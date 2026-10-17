package expense

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSetMemberName(t *testing.T) {
	for _, rawName := range []string{"  Анна  ", "\u00a0Анна\u2003", strings.Repeat("я", MaxDisplayNameLength), "", " \t\n"} {
		t.Run(rawName, func(t *testing.T) {
			repository := &countingRepo{}
			member, err := NewService(repository).SetMemberName(context.Background(), "9223372036854775807", &rawName)
			if err != nil {
				t.Fatal(err)
			}
			if repository.calls != 1 || member.UserID != 9223372036854775807 {
				t.Fatal("member ID changed or write was not delegated")
			}
			want := strings.TrimSpace(rawName)
			if want == "" {
				if member.DisplayName != nil {
					t.Fatal("blank name was not cleared")
				}
			} else if member.DisplayName == nil || *member.DisplayName != want {
				t.Fatal("name was not trimmed or Unicode length was counted incorrectly")
			}
		})
	}
}

func TestSetMemberNameRejectsInvalidInput(t *testing.T) {
	for _, name := range []string{strings.Repeat("я", 101), "Анна\nИванова", "Анна\tИванова", "Анна\x00", "\xff"} {
		repository := &countingRepo{}
		_, err := NewService(repository).SetMemberName(context.Background(), "101", &name)
		var fields ValidationError
		if !errors.As(err, &fields) || fields["display_name"] == "" || repository.calls != 0 {
			t.Fatal("invalid name was not rejected before persistence")
		}
	}
	for _, id := range []string{"", "0", "01", "9223372036854775808"} {
		repository := &countingRepo{}
		_, err := NewService(repository).SetMemberName(context.Background(), id, nil)
		var fields ValidationError
		if !errors.As(err, &fields) || fields["user_id"] == "" || repository.calls != 0 {
			t.Fatal("invalid ID reached persistence")
		}
	}
}

func TestSetMemberNameClearAndRepositoryError(t *testing.T) {
	wantError := errors.New("database unavailable")
	for _, repositoryError := range []error{nil, wantError} {
		repository := &countingRepo{err: repositoryError}
		_, err := NewService(repository).SetMemberName(context.Background(), "101", nil)
		if !errors.Is(err, repositoryError) || repository.calls != 1 || repository.member.DisplayName != nil {
			t.Fatal("clear or repository error was not preserved")
		}
	}
}
