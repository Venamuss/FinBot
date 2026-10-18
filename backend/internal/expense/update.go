package expense

import "strings"

// UpdateInput contains only the requested changes. A nil field is omitted;
// CommentSet distinguishes an omitted comment from an explicit clear.
type UpdateInput struct {
	UserID     *string
	Amount     *string
	Category   *string
	SpentAt    *string
	Comment    *string
	CommentSet bool
}

// Changes holds validated values for an atomic partial update.
type Changes struct {
	UserID     *int64
	Amount     *Amount
	Category   *Category
	SpentAt    *Date
	Comment    *string
	CommentSet bool
}

func (input UpdateInput) Validate() (Changes, error) {
	if input.UserID == nil && input.Amount == nil && input.Category == nil && input.SpentAt == nil && !input.CommentSet {
		return Changes{}, ValidationError{}
	}

	changes := Changes{Comment: input.Comment, CommentSet: input.CommentSet}
	fields := ValidationError{}

	if input.UserID != nil {
		value, err := ParseID(*input.UserID)
		if err != nil {
			fields["user_id"] = err.Error()
		}
		changes.UserID = &value
	}

	if input.Amount != nil {
		value, err := ParseAmount(*input.Amount)
		if err != nil {
			fields["amount"] = err.Error()
		}
		changes.Amount = &value
	}

	if input.Category != nil {
		value, err := ParseCategory(*input.Category)
		if err != nil {
			fields["category"] = err.Error()
		}
		changes.Category = &value
	}

	if input.SpentAt != nil {
		value, err := ParseDate(*input.SpentAt)
		if err != nil {
			fields["spent_at"] = err.Error()
		}
		changes.SpentAt = &value
	}

	if input.CommentSet && input.Comment != nil && strings.ContainsRune(*input.Comment, 0) {
		fields["comment"] = ErrInvalidComment.Error()
	}

	if len(fields) != 0 {
		return Changes{}, fields
	}

	return changes, nil
}
