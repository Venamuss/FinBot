package conversation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sort"

	"github.com/Venamuss/FinBot/internal/expense"
	"github.com/Venamuss/FinBot/internal/llm"
)

var errArguments = errors.New("invalid conversation tool arguments")

// argumentError carries a fixed diagnostic category, never an argument value.
type argumentError struct {
	category string
}

func (err *argumentError) Error() string {
	return errArguments.Error()
}

func (err *argumentError) Unwrap() error {
	return errArguments
}

func invalidArguments(category string) error {
	return &argumentError{category: category}
}

type toolSpec struct {
	description string
	fields      []string
	required    []string
	strict      bool
}

var toolSpecs = map[string]toolSpec{
	"create_expense": {
		description: "Create one expense from a clear description and amount, including <description> <amount> without a currency marker. Extract amount independently of category; use другое if uncertain. Put only the description in comment, excluding the extracted price and currency; preserve model numbers and quantities. Include amount_text as the exact source price expression (including за and currency when present). Use today if no date. Clarify only missing/ambiguous amounts or unsupported currency. RUB only; never convert.",
		fields:      []string{"amount", "amount_text", "category", "spent_at", "comment", "currency", "request_context"},
		required:    []string{"amount", "spent_at"},
	},
	"list_expenses": {
		description: `Start a new listing of own expenses at the first page: "мои траты", "мои расходы", "траты", "покажи расходы". No period requested means ALL dates; set dates and period to null. Independent requests never inherit previous filters. Reuse previous_listing filters only for explicit refinements such as "из них только еда"; refinements also start at the first page. For next/previous pages use navigate_expenses instead. One call covers all categories. Set unrequested arguments to null. Use period OR date_from/date_to, never both. за неделю = last_seven_days; на этой неделе = this_week; за месяц = this_month. "последние 5 трат" = limit "5", no dates.`,
		fields:      []string{"period", "date_from", "date_to", "category", "query", "limit"},
		strict:      true,
	},
	"navigate_expenses": {
		description: `Navigate the previous listing without changing its filters or page size. "ещё", "дальше", "следующая страница" = next; "назад", "предыдущая страница" = previous. The server calculates page positions; never invent offsets.`,
		fields:      []string{"direction"},
		required:    []string{"direction"},
		strict:      true,
	},
	"total_expenses": {
		description: "Calculate own total, not a list. No date means today. Set unrequested filters to null. Use period OR date_from/date_to, never both. в этом месяце = this_month.",
		fields:      []string{"period", "date_from", "date_to", "category", "query"},
		strict:      true,
	},
	"update_expense": {
		description: "Update one own expense, never create a replacement. Use id for an explicit ID, query for its description, or null for both for last/focused. Include previous_amount only when stated. Supply only changed fields; null means unchanged, including comment. To explicitly clear a comment use an empty string. Use request_context=new for an independent command, followup only for a pending field/target answer. RUB only; clarify explicit unsupported currency before updating.",
		fields:      []string{"id", "query", "previous_amount", "amount", "category", "spent_at", "comment", "currency", "request_context"},
		strict:      true,
	},
	"delete_expense": {
		description: "Request deletion of one own expense. The server resolves the target and always asks for explicit yes/no confirmation before deleting. A candidate choice only selects a target. Use query for its description, id for an explicit ID, or null for both for an explicit last/focused reference. Clarify a missing reference with intent=delete_expense. Use request_context=new for an independent command, followup only for a pending target answer.",
		fields:      []string{"id", "query", "request_context"},
		strict:      true,
	},
	"choose_expense": {
		description: "Resolve an active expense selection/confirmation only. choice is its 1-based displayed position, never an expense ID, amount or category. Use cancel only for explicit cancellation of that selection.",
		fields:      []string{"choice"},
		required:    []string{"choice"},
	},
	"clarify": {
		description: "Ask only for missing or genuinely ambiguous information. Use amount for missing/ambiguous amounts, currency for explicit unsupported currencies. An unfamiliar description is not a reason to ask for an amount or category; use category другое. Never claim an operation succeeded.",
		fields:      []string{"field", "request_context", "intent", "amount", "amount_text", "category", "spent_at", "comment", "currency", "id", "query", "previous_amount"},
		required:    []string{"field"},
	},
	"answer_clarification": {
		description: "Answer the current pending_field only, using a canonical value. Never select an expense or change unrelated fields. For currency use value RUB only for an explicit RUB correction; amount may optionally correct its numeric value. A bare number or yes does not answer a currency question.",
		fields:      []string{"value", "amount"},
		required:    []string{"value"},
	},
	"cancel_request": {
		description: "Cancel an active pending clarification or selection only when the user explicitly asks to cancel. Never use for a number, category or an unfamiliar description.",
	},
}

type action struct {
	name string
	args map[string]*string
}

func toolsForModel() []llm.Tool {
	names := make([]string, 0, len(toolSpecs))
	for name := range toolSpecs {
		names = append(names, name)
	}
	sort.Strings(names)
	tools := make([]llm.Tool, 0, len(names))
	for _, name := range names {
		spec := toolSpecs[name]
		properties := make(map[string]any, len(spec.fields))
		for _, field := range spec.fields {
			property := map[string]any{"type": "string", "maxLength": 4096}
			if field != "comment" {
				property["minLength"] = 1
			}
			if field == "comment" || (spec.strict && !contains(spec.required, field)) {
				property["type"] = []string{"string", "null"}
			}
			switch field {
			case "intent":
				property["enum"] = []string{"create_expense", "update_expense", "delete_expense"}
				property["description"] = "Operation being clarified. Include all already known valid values in the other arguments. For creation, category is inferred or defaults to другое; never ask for a creation category."
			case "value":
				property["description"] = "Canonical answer to pending_field: decimal amount, category, date (YYYY-MM-DD/today/yesterday), or RUB for an explicit currency correction."
			case "id":
				property["pattern"] = "^[1-9][0-9]*$"
			case "amount", "previous_amount":
				property["pattern"] = `^(0|[1-9][0-9]{0,11})(\.[0-9]{1,2})?$`
				property["description"] = "Exact positive RUB decimal string, 0.01..999999999999.99. Normalize a user comma separator to a period. No currency symbols, grouping, rounding or exponent notation."
			case "comment":
				property["description"] = "Expense description only, without the extracted price, its currency marker or price preposition. Preserve the user's language, meaningful words, product/model numbers and quantities. Never copy the whole message or invent a description. Omit or use null when absent. Explicit comment edits may retain any requested text."
				if name == "update_expense" {
					property["description"] = "Only an explicitly requested description edit. Null leaves the comment unchanged; an empty string explicitly clears it. Never copy the target description into a change."
				}
			case "amount_text":
				property["description"] = "For creation, copy the exact price expression from the user's text, including за and RUB marker when present, e.g. 6050 or за 500 рублей. Must equal amount exactly; never include description words, model numbers or quantities. Supply whenever the source contains the price. Omit while its amount is unresolved."
			case "currency":
				property["enum"] = []string{"RUB"}
				property["description"] = "Supported currency code, always RUB. An absent currency defaults to RUB; never clarify merely because no marker was given. For an explicit unsupported code, name or symbol, use clarify(field=currency)."
			case "request_context":
				property["enum"] = []string{"new", "followup"}
				property["description"] = "Use new for an independent request, discarding pending data. Use followup only to answer pending_request. Creation defaults to new when omitted. Prefer answer_clarification for a pending field; new expenses without currency markers use RUB."
			case "choice":
				property["pattern"] = "^(cancel|[1-9][0-9]*)$"
			case "field":
				property["enum"] = []string{"amount", "currency", "category", "spent_at", "target", "request"}
			case "category":
				property["enum"] = []string{
					string(expense.CategoryFood),
					string(expense.CategoryTransport),
					string(expense.CategoryHousing),
					string(expense.CategoryEntertainment),
					string(expense.CategoryHealth),
					string(expense.CategoryShopping),
					string(expense.CategoryOther),
				}
				if name == "create_expense" {
					property["description"] = "Infer independently of amount. Use другое if the expense description is unfamiliar; do not ask for a category."
				}
				if name == "list_expenses" || name == "total_expenses" {
					property["description"] = "Null unless the user requests a particular category. Null includes all categories."
				}
			case "period":
				property["enum"] = []string{"today", "yesterday", "last_seven_days", "this_week", "this_month"}
				property["description"] = "Relative date range resolved by the server. Null for all dates in a list; totals default to today. Do not combine with date_from or date_to."
			case "limit":
				property["pattern"] = "^([1-9]|1[0-9]|20)$"
				property["description"] = "Page size as a decimal string, 1..20. Null defaults to 20. Use 5 for последние 5 трат."
			case "direction":
				property["enum"] = []string{"next", "previous"}
			case "date_from", "date_to":
				property["pattern"] = `^[0-9]{4}-[0-9]{2}-[0-9]{2}$`
				property["description"] = "Inclusive YYYY-MM-DD boundary, only for an explicit date range. Null when using period."
			case "query":
				property["description"] = "Literal comment substring explicitly requested by the user. Omit unless needed; never use a wildcard or the whole request."
				if name == "list_expenses" || name == "total_expenses" {
					property["description"] = "Literal comment substring explicitly requested by the user. Null for all expenses; never use a wildcard or the whole request."
				} else if name == "update_expense" || name == "delete_expense" {
					property["description"] = "Literal identifying description substring, excluding command words. Null when using an explicit id or last/focused expense. Never use a wildcard or the whole request."
				}
			case "spent_at":
				property["description"] = "YYYY-MM-DD or today/yesterday. Relative values are resolved by the server."
			}
			if spec.strict && !contains(spec.required, field) {
				if values, ok := property["enum"].([]string); ok {
					nullableValues := make([]any, 0, len(values)+1)
					for _, value := range values {
						nullableValues = append(nullableValues, value)
					}
					property["enum"] = append(nullableValues, nil)
				}
			}
			properties[field] = property
		}
		required := spec.required
		if spec.strict {
			// Strict schemas require every property; null represents an unused argument.
			required = spec.fields
		}
		if required == nil {
			required = []string{}
		}
		// This schema contains only strings, slices, and maps, so Marshal cannot fail.
		schema, _ := json.Marshal(map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             required,
			"additionalProperties": false,
		})
		tools = append(tools, llm.Tool{
			Type: "function",
			Function: llm.Function{
				Name:        name,
				Description: spec.description,
				Parameters:  schema,
				Strict:      spec.strict,
			},
		})
	}
	return tools
}

func parseAction(response llm.Response) (action, error) {
	if len(response.ToolCalls) != 1 {
		return action{}, invalidArguments("tool_call_count")
	}
	if response.FinishReason != "tool_calls" {
		return action{}, invalidArguments("finish_reason")
	}
	call := response.ToolCalls[0]
	spec, ok := toolSpecs[call.Name]
	if !ok {
		return action{}, invalidArguments("unknown_tool")
	}
	decoder := json.NewDecoder(bytes.NewReader(call.Arguments))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return action{}, invalidArguments("json")
	}
	args := make(map[string]*string)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return action{}, invalidArguments("json")
		}
		name, ok := token.(string)
		if !ok {
			return action{}, invalidArguments("json")
		}
		if _, duplicate := args[name]; duplicate {
			return action{}, invalidArguments("duplicate_field")
		}
		if !contains(spec.fields, name) {
			return action{}, invalidArguments("unknown_field")
		}
		var value *string
		if err := decoder.Decode(&value); err != nil {
			return action{}, invalidArguments("argument_type")
		}
		if value == nil && name != "comment" && (!spec.strict || contains(spec.required, name)) {
			return action{}, invalidArguments("null_argument")
		}
		if value != nil && (len(*value) > 4096 || (name != "comment" && *value == "")) {
			return action{}, invalidArguments("argument_length")
		}
		args[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return action{}, invalidArguments("json")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return action{}, invalidArguments("json")
	}
	for _, field := range spec.required {
		if args[field] == nil {
			return action{}, invalidArguments("missing_required")
		}
	}
	if args["id"] != nil && args["query"] != nil {
		return action{}, invalidArguments("conflicting_target")
	}
	if value := args["request_context"]; value != nil && *value != "new" && *value != "followup" {
		return action{}, invalidArguments("enum")
	}
	if value := args["direction"]; value != nil && *value != "next" && *value != "previous" {
		return action{}, invalidArguments("enum")
	}
	if value := args["intent"]; value != nil && !contains([]string{"create_expense", "update_expense", "delete_expense"}, *value) {
		return action{}, invalidArguments("enum")
	}
	if value := args["field"]; value != nil && !contains([]string{"amount", "currency", "category", "spent_at", "target", "request"}, *value) {
		return action{}, invalidArguments("enum")
	}
	if call.Name == "update_expense" || call.Name == "delete_expense" {
		// Nullable placeholders are not changes and must not erase stored fields.
		for field, value := range args {
			if value == nil {
				delete(args, field)
			}
		}
	}
	return action{name: call.Name, args: args}, nil
}

func contains(values []string, value string) bool {
	return slices.Contains(values, value)
}

func (operation action) value(key string) string {
	if operation.args[key] == nil {
		return ""
	}
	return *operation.args[key]
}
