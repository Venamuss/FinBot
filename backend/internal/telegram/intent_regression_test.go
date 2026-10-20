package telegram

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestUnclearInputAndNaturalCorrectionsIntegration(t *testing.T) {
	workflow := newModificationWorkflow(t)
	workflow.send(101, "200 водка", "create_expense", `{"amount":"200","amount_text":"200","spent_at":"today","comment":"водка"}`)
	workflow.send(202, "кофе 900", "create_expense", `{"amount":"900","spent_at":"today","comment":"кофе"}`)
	original := workflow.item(1)
	for _, correction := range []struct{ text, amount string }{
		{"последний был 400", "400"},
		{"последний был 500", "500"},
		{"последняя трата была 450", "450"},
		{"последний расход на самом деле 600", "600"},
	} {
		reply := workflow.send(101, correction.text, "update_expense", `{"amount":"`+correction.amount+`"}`)
		item := workflow.item(1)
		if !strings.Contains(reply, "Расход обновлён") || item.Amount.String() != correction.amount+".00" || workflow.count(101) != 1 || workflow.item(2).Amount.String() != "900.00" {
			t.Fatal("natural correction failed or affected another owner:", reply)
		}
		original.Amount = item.Amount
		original.UpdatedAt = item.UpdatedAt
		if !reflect.DeepEqual(original, item) {
			t.Fatal("amount correction changed unrelated fields")
		}
	}
	committed := workflow.update
	snapshot := expenseSnapshot(t, workflow)
	calls := workflow.calls.Load()
	if err := workflow.bot.Process(context.Background(), committed); err != nil {
		t.Fatal(err)
	}
	<-workflow.replies
	if workflow.calls.Load() != calls || expenseSnapshot(t, workflow) != snapshot {
		t.Fatal("duplicate correction reran model or mutation")
	}

	for _, text := range []string{"ыфваываы", "лвлала", "asdfghjkl", "?!@#$%"} {
		reply := workflow.send(101, text, "list_expenses", `{}`)
		if reply != "Не понял запрос. Попробуйте переформулировать." || expenseSnapshot(t, workflow) != snapshot {
			t.Fatal("incorrect list proposal was not rejected:", reply)
		}
		calls = workflow.calls.Load()
		if err := workflow.bot.Process(context.Background(), workflow.update); err != nil {
			t.Fatal(err)
		}
		if replay := <-workflow.replies; replay != reply || workflow.calls.Load() != calls {
			t.Fatal("unknown-intent replay called the model again")
		}
		workflow.send(101, text, "update_expense", `{"amount":"999"}`)
		if expenseSnapshot(t, workflow) != snapshot {
			t.Fatal("nonsense caused a model-generated mutation")
		}
	}

	calls = workflow.calls.Load()
	workflow.send(303, "последний был 999", "update_expense", `{"amount":"999"}`)
	if workflow.calls.Load() != calls || expenseSnapshot(t, workflow) != snapshot {
		t.Fatal("denied user reached model or expenses")
	}
}

func TestUnclearInputKeepsConfirmationAndFocusIntegration(t *testing.T) {
	workflow := newModificationWorkflow(t)
	workflow.send(101, "кофе 350", "create_expense", `{"amount":"350","spent_at":"yesterday","comment":"кофе"}`)
	workflow.send(101, "такси 900", "create_expense", `{"amount":"900","spent_at":"today","comment":"такси"}`)
	workflow.send(101, "покажи кофе", "list_expenses", `{"query":"кофе"}`)
	workflow.send(101, "последний был 400", "update_expense", `{"amount":"400"}`)
	if workflow.item(1).Amount.String() != "400.00" || workflow.item(2).Amount.String() != "900.00" {
		t.Fatal("selected focus was lost")
	}
	workflow.send(101, "удали выбранную трату", "delete_expense", `{}`)
	before := expenseSnapshot(t, workflow)
	workflow.send(101, "лвлала", "list_expenses", `{}`)
	if expenseSnapshot(t, workflow) != before {
		t.Fatal("unclear text confirmed deletion")
	}
	workflow.send(101, "нет", "delete_expense", `{}`)
	workflow.send(101, "да", "delete_expense", `{}`)
	workflow.send(101, "стоп", "list_expenses", `{}`)
	workflow.send(101, "стоп", "list_expenses", `{}`)
	if expenseSnapshot(t, workflow) != before {
		t.Fatal("declining or repeated controls mutated expenses")
	}
	workflow.send(101, "удали выбранную трату", "delete_expense", `{}`)
	workflow.send(101, "asdfghjkl", "unknown_intent", `{}`)
	workflow.send(101, "да", "delete_expense", `{}`)
	if workflow.count(101) != 1 || workflow.item(2).Amount.String() != "900.00" {
		t.Fatal("explicit confirmation did not delete the selected expense")
	}
}
