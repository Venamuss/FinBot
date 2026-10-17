package expense

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type ownerRepoStub struct {
	calls   int
	owner   int64
	id      int64
	filter  OwnerFilter
	limit   int
	offset  int
	version time.Time
	changes Changes
	err     error
}

func (r *ownerRepoStub) GetOwned(_ context.Context, owner, id int64) (Expense, error) {
	r.calls++
	r.owner = owner
	r.id = id
	return Expense{ID: id, UserID: owner}, r.err
}

func (r *ownerRepoStub) ListOwned(_ context.Context, owner int64, filter OwnerFilter, limit, offset int) (Page, error) {
	r.calls++
	r.owner = owner
	r.filter = filter
	r.limit = limit
	r.offset = offset
	return Page{Items: []Expense{}, Limit: limit, Offset: offset}, r.err
}

func (r *ownerRepoStub) TotalOwned(_ context.Context, owner int64, filter OwnerFilter) (string, error) {
	r.calls++
	r.owner = owner
	r.filter = filter
	return "0.30", r.err
}

func (r *ownerRepoStub) LatestOwned(_ context.Context, owner int64) (Expense, error) {
	r.calls++
	r.owner = owner
	return Expense{ID: 42, UserID: owner}, r.err
}

func (r *ownerRepoStub) UpdateOwned(_ context.Context, owner, id int64, version time.Time, changes Changes) (Expense, error) {
	r.calls++
	r.owner = owner
	r.id = id
	r.version = version
	r.changes = changes
	return Expense{ID: id, UserID: owner}, r.err
}

func (r *ownerRepoStub) DeleteOwned(_ context.Context, owner, id int64, version time.Time) error {
	r.calls++
	r.owner = owner
	r.id = id
	r.version = version
	return r.err
}

func TestOwnerServiceOperations(t *testing.T) {
	ctx := context.Background()
	version := time.Date(2026, 10, 17, 9, 0, 0, 123000, time.UTC)
	filter := OwnerFilter{DateFrom: "2026-10-01", DateTo: "2026-10-17", Category: CategoryFood}
	amount := "0.30"
	for _, repositoryError := range []error{nil, ErrNotFound, errors.New("database unavailable")} {
		repo := &ownerRepoStub{err: repositoryError}
		service := NewOwnerService(repo)
		item, err := service.GetByID(ctx, 101, 42)
		if !errors.Is(err, repositoryError) || item.UserID != 101 || repo.id != 42 {
			t.Fatalf("get: %+v, %v", item, err)
		}
		page, err := service.List(ctx, 101, filter, 10, 2)
		if !errors.Is(err, repositoryError) || page.Items == nil || repo.filter != filter || repo.limit != 10 || repo.offset != 2 {
			t.Fatalf("list: %+v, %v", page, err)
		}
		total, err := service.Total(ctx, 101, filter)
		if !errors.Is(err, repositoryError) || total != "0.30" || repo.filter != filter {
			t.Fatalf("total: %s, %v", total, err)
		}
		item, err = service.Latest(ctx, 101)
		if !errors.Is(err, repositoryError) || item.UserID != 101 {
			t.Fatalf("latest: %+v, %v", item, err)
		}
		_, err = service.Update(ctx, 101, 42, version, OwnerUpdateInput{Amount: &amount, CommentSet: true})
		if !errors.Is(err, repositoryError) || !repo.version.Equal(version) || repo.id != 42 {
			t.Fatalf("update: %v", err)
		}
		wantAmount := Amount(30)
		if !reflect.DeepEqual(repo.changes, Changes{Amount: &wantAmount, CommentSet: true}) {
			t.Fatalf("update changed omitted fields: %+v", repo.changes)
		}
		err = service.Delete(ctx, 101, 43, version)
		if !errors.Is(err, repositoryError) || repo.id != 43 || !repo.version.Equal(version) || repo.owner != 101 || repo.calls != 6 {
			t.Fatalf("delete or owner delegation: %+v, %v", repo, err)
		}
	}
}

func TestOwnerServiceRejectsInvalidInput(t *testing.T) {
	ctx := context.Background()
	version := time.Now()
	amount := "1.001"
	validAmount := "1"
	badDate := "2026-02-29"
	badCategory := "unknown"
	badComment := "\x00"
	repo := &ownerRepoStub{}
	service := NewOwnerService(repo)
	for _, owner := range []int64{0, -1} {
		_, getErr := service.GetByID(ctx, owner, 42)
		_, listErr := service.List(ctx, owner, OwnerFilter{}, 50, 0)
		_, totalErr := service.Total(ctx, owner, OwnerFilter{})
		_, latestErr := service.Latest(ctx, owner)
		_, updateErr := service.Update(ctx, owner, 42, version, OwnerUpdateInput{Amount: &validAmount})
		deleteErr := service.Delete(ctx, owner, 42, version)
		for _, err := range []error{getErr, listErr, totalErr, latestErr, updateErr, deleteErr} {
			if !errors.Is(err, ErrInvalidID) {
				t.Fatalf("invalid owner accepted: %v", err)
			}
		}
	}
	for _, id := range []int64{0, -1} {
		_, getErr := service.GetByID(ctx, 101, id)
		_, updateErr := service.Update(ctx, 101, id, version, OwnerUpdateInput{Amount: &validAmount})
		deleteErr := service.Delete(ctx, 101, id, version)
		for _, err := range []error{getErr, updateErr, deleteErr} {
			if !errors.Is(err, ErrInvalidID) {
				t.Fatalf("invalid expense ID accepted: %v", err)
			}
		}
	}
	for _, filter := range []OwnerFilter{
		{DateFrom: "2026-02-29"}, {DateTo: "garbage"},
		{DateFrom: "2026-10-17", DateTo: "2026-10-16"}, {Category: "unknown"},
	} {
		_, listErr := service.List(ctx, 101, filter, 50, 0)
		_, totalErr := service.Total(ctx, 101, filter)
		for _, err := range []error{listErr, totalErr} {
			var fields ValidationError
			if !errors.As(err, &fields) {
				t.Fatalf("invalid filter accepted: %v", err)
			}
		}
	}
	for _, page := range [][2]int{{0, 0}, {101, 0}, {50, -1}, {50, 2147483648}} {
		if _, err := service.List(ctx, 101, OwnerFilter{}, page[0], page[1]); !errors.Is(err, ErrInvalidPagination) {
			t.Fatalf("invalid pagination: %v", err)
		}
	}
	for _, input := range []OwnerUpdateInput{
		{}, {Amount: &amount}, {Amount: &validAmount, SpentAt: &badDate},
		{Category: &badCategory}, {CommentSet: true, Comment: &badComment},
	} {
		_, err := service.Update(ctx, 101, 42, version, input)
		var fields ValidationError
		if !errors.As(err, &fields) {
			t.Fatalf("invalid update accepted: %v", err)
		}
	}
	_, updateErr := service.Update(ctx, 101, 42, time.Time{}, OwnerUpdateInput{Amount: &validAmount})
	deleteErr := service.Delete(ctx, 101, 42, time.Time{})
	if !errors.Is(updateErr, ErrInvalidVersion) || !errors.Is(deleteErr, ErrInvalidVersion) {
		t.Fatalf("missing version accepted: update %v, delete %v", updateErr, deleteErr)
	}
	if repo.calls != 0 {
		t.Fatal("invalid input reached the repository")
	}
}
