package expense

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestUpdateValidation(t *testing.T) {
	owner := "9223372036854775807"
	amount := "350.5"
	category := "транспорт"
	date := "2024-02-29"
	comment := ""
	input := UpdateInput{
		UserID: &owner, Amount: &amount, Category: &category, SpentAt: &date,
		CommentSet: true, Comment: &comment,
	}
	changes, err := input.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if changes.UserID == nil || *changes.UserID != 9223372036854775807 {
		t.Fatal("owner lost")
	}
	if changes.Amount == nil || *changes.Amount != 35050 {
		t.Fatal("amount lost precision")
	}
	if changes.Category == nil || *changes.Category != CategoryTransport {
		t.Fatal("category lost")
	}
	if changes.SpentAt == nil || *changes.SpentAt != Date(date) {
		t.Fatal("calendar date changed")
	}
	if !changes.CommentSet || changes.Comment == nil || *changes.Comment != "" {
		t.Fatal("empty comment lost")
	}
}

func TestUpdatePresence(t *testing.T) {
	amount := "1"
	comment := "synthetic"
	tests := []struct {
		name  string
		input UpdateInput
		want  Changes
	}{
		{name: "clear comment", input: UpdateInput{CommentSet: true}, want: Changes{CommentSet: true}},
		{name: "set comment", input: UpdateInput{CommentSet: true, Comment: &comment}, want: Changes{CommentSet: true, Comment: &comment}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.input.Validate()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("want %+v, got %+v", tc.want, got)
			}
		})
	}
	repository := &countingRepo{}
	service := NewService(repository)
	got, err := service.Update(context.Background(), 42, UpdateInput{Amount: &amount})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 42 || repository.id != 42 || repository.calls != 1 {
		t.Fatal("update was not delegated once with the requested ID")
	}
	changes := repository.changes
	if changes.Amount == nil || *changes.Amount != 100 || changes.UserID != nil || changes.Category != nil || changes.SpentAt != nil || changes.CommentSet {
		t.Fatalf("omitted fields changed: %+v", changes)
	}
}

func TestServiceRejectsInvalidUpdates(t *testing.T) {
	badOwner := "01"
	badAmount := "1.001"
	badCategory := "Еда"
	badDate := "2026-02-29"
	badComment := "\x00"
	goodAmount := "2"
	tests := []struct {
		name       string
		input      UpdateInput
		fieldCount int
	}{
		{name: "empty", input: UpdateInput{}},
		{name: "owner", input: UpdateInput{UserID: &badOwner}, fieldCount: 1},
		{name: "amount", input: UpdateInput{Amount: &badAmount}, fieldCount: 1},
		{name: "category", input: UpdateInput{Category: &badCategory}, fieldCount: 1},
		{name: "date", input: UpdateInput{SpentAt: &badDate}, fieldCount: 1},
		{name: "comment", input: UpdateInput{CommentSet: true, Comment: &badComment}, fieldCount: 1},
		{name: "mixed valid and invalid", input: UpdateInput{Amount: &goodAmount, SpentAt: &badDate}, fieldCount: 1},
		{name: "all invalid", input: UpdateInput{UserID: &badOwner, Amount: &badAmount, Category: &badCategory, SpentAt: &badDate}, fieldCount: 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repository := &countingRepo{}
			_, err := NewService(repository).Update(context.Background(), 42, tc.input)
			var fields ValidationError
			if !errors.As(err, &fields) || len(fields) != tc.fieldCount {
				t.Fatalf("expected %d validation errors, got %v", tc.fieldCount, err)
			}
			if repository.calls != 0 {
				t.Fatal("invalid update reached repository")
			}
		})
	}
}

func TestServiceCRUDDelegation(t *testing.T) {
	ctx := context.Background()
	amount := "1"
	for _, repositoryError := range []error{nil, ErrNotFound, errors.New("database unavailable")} {
		repository := &countingRepo{err: repositoryError}
		service := NewService(repository)
		_, err := service.Update(ctx, 42, UpdateInput{Amount: &amount})
		if !errors.Is(err, repositoryError) {
			t.Fatalf("update error: %v", err)
		}
		if err := service.Delete(ctx, 43); !errors.Is(err, repositoryError) {
			t.Fatalf("delete error: %v", err)
		}
		if repository.id != 43 {
			t.Fatal("delete ID changed")
		}
		members, err := service.ListMembers(ctx)
		if !errors.Is(err, repositoryError) || !reflect.DeepEqual(members, []int64{2, 10}) {
			t.Fatalf("members: %v, %v", members, err)
		}
		if repository.calls != 3 {
			t.Fatalf("want 3 repository calls, got %d", repository.calls)
		}
	}
}
