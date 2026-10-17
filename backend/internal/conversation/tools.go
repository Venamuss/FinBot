package conversation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sort"

	"github.com/Venamuss/FinBot/internal/llm"
)

var errArguments = errors.New("invalid conversation tool arguments")

type toolSpec struct {
	description string
	fields      []string
	required    []string
	strict      bool
}

var toolSpecs = map[string]toolSpec{
	"create_expense": {
		description: "Create one expense. Infer category; use today when date is omitted. Ask for missing amount.",
		fields:      []string{"amount", "category", "spent_at", "comment"},
		required:    []string{"amount", "category", "spent_at"},
	},
	"list_expenses": {
		description: `List own expenses: "мои траты", "мои расходы", "покажи все расходы за сегодня". No date means today, not clarification. One call covers all categories. Set unrequested filters to null. Use period OR date_from/date_to, never both. за неделю = last_seven_days; на этой неделе = this_week.`,
		fields:      []string{"period", "date_from", "date_to", "category", "query"},
		strict:      true,
	},
	"total_expenses": {
		description: "Calculate own total, not a list. No date means today. Set unrequested filters to null. Use period OR date_from/date_to, never both. в этом месяце = this_month.",
		fields:      []string{"period", "date_from", "date_to", "category", "query"},
		strict:      true,
	},
	"update_expense": {
		description: "Update one expense. Omit id/query for last or focused expense. Include previous_amount when stated. query searches the comment.",
		fields:      []string{"id", "query", "previous_amount", "amount", "category", "spent_at", "comment"},
	},
	"delete_expense": {
		description: "Delete one expense. Use query for a description such as такси, id for an explicit ID, or neither for last/focused expense.",
		fields:      []string{"id", "query"},
	},
	"choose_expense": {
		description: "Resolve a pending selection using its 1-based choice number, only when the user chooses or explicitly confirms it. Use cancel when the user cancels the pending action.",
		fields:      []string{"choice"},
		required:    []string{"choice"},
	},
	"clarify": {
		description: "Ask for missing information; never claim an operation succeeded.",
		fields:      []string{"field"},
		required:    []string{"field"},
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
			case "id":
				property["pattern"] = "^[1-9][0-9]*$"
			case "amount", "previous_amount":
				property["pattern"] = `^(0|[1-9][0-9]*)(\.[0-9]{1,2})?$`
			case "choice":
				property["pattern"] = "^(cancel|[1-9][0-9]*)$"
			case "field":
				property["enum"] = []string{"amount", "category", "spent_at", "target", "request"}
			case "category":
				property["enum"] = []string{"еда", "транспорт", "жильё", "развлечения", "здоровье", "покупки", "другое"}
				if spec.strict {
					property["description"] = "Null unless the user requests a particular category. Null includes all categories."
				}
			case "period":
				property["enum"] = []string{"today", "yesterday", "last_seven_days", "this_week", "this_month"}
				property["description"] = "Relative date range resolved by the server. Default today. Do not combine with date_from or date_to."
			case "date_from", "date_to":
				property["pattern"] = `^[0-9]{4}-[0-9]{2}-[0-9]{2}$`
				property["description"] = "Inclusive YYYY-MM-DD boundary, only for an explicit date range. Null when using period."
			case "query":
				property["description"] = "Literal comment substring explicitly requested by the user. Omit unless needed; never use a wildcard or the whole request."
				if spec.strict {
					property["description"] = "Literal comment substring explicitly requested by the user. Null for all expenses; never use a wildcard or the whole request."
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
			// Strict schemas require every property; null represents an unused filter.
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
	if len(response.ToolCalls) != 1 || response.FinishReason != "tool_calls" {
		return action{}, errArguments
	}
	call := response.ToolCalls[0]
	spec, ok := toolSpecs[call.Name]
	if !ok {
		return action{}, errArguments
	}
	decoder := json.NewDecoder(bytes.NewReader(call.Arguments))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return action{}, errArguments
	}
	args := make(map[string]*string)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return action{}, errArguments
		}
		name, ok := token.(string)
		if !ok {
			return action{}, errArguments
		}
		if _, duplicate := args[name]; duplicate || !contains(spec.fields, name) {
			return action{}, errArguments
		}
		var value *string
		if err := decoder.Decode(&value); err != nil {
			return action{}, errArguments
		}
		if value == nil && name != "comment" && (!spec.strict || contains(spec.required, name)) {
			return action{}, errArguments
		}
		if value != nil && (len(*value) > 4096 || (name != "comment" && *value == "")) {
			return action{}, errArguments
		}
		args[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return action{}, errArguments
	}
	if _, err := decoder.Token(); err != io.EOF {
		return action{}, errArguments
	}
	for _, field := range spec.required {
		if args[field] == nil {
			return action{}, errArguments
		}
	}
	if args["id"] != nil && args["query"] != nil {
		return action{}, errArguments
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
