package expense

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxDisplayNameLength = 100

// Member contains presentation metadata, never authorization or expense ownership.
type Member struct {
	UserID      int64
	DisplayName *string
}

func (s *Service) SetMemberName(ctx context.Context, userID string, displayName *string) (Member, error) {
	id, err := ParseID(userID)
	if err != nil {
		return Member{}, ValidationError{"user_id": err.Error()}
	}

	name, err := normalizeDisplayName(displayName)
	if err != nil {
		return Member{}, ValidationError{"display_name": err.Error()}
	}

	return s.repo.SetMemberName(ctx, Member{UserID: id, DisplayName: name})
}

func normalizeDisplayName(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	name := strings.TrimSpace(*value)

	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxDisplayNameLength {
		return nil, ErrInvalidDisplayName
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return nil, ErrInvalidDisplayName
		}
	}

	if name == "" {
		return nil, nil
	}
	return &name, nil
}
