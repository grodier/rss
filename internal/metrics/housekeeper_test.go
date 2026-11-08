package metrics

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeHousekeepingStore struct {
	rollUpErr                 error
	rolledUpBefore            time.Time
	deleted                   bool
	minutes, hours, runtimeTo time.Time
}

func (f *fakeHousekeepingStore) RollUp(ctx context.Context, before time.Time) error {
	f.rolledUpBefore = before
	return f.rollUpErr
}

func (f *fakeHousekeepingStore) DeleteBefore(ctx context.Context, minutes, hours, runtime time.Time) error {
	f.deleted = true
	f.minutes, f.hours, f.runtimeTo = minutes, hours, runtime
	return nil
}

type fakeErrorTrimmer struct {
	err  error
	keep int
}

func (f *fakeErrorTrimmer) Trim(ctx context.Context, keep int) error {
	f.keep = keep
	return f.err
}

func TestTidy(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC)
	store := &fakeHousekeepingStore{}
	trimmer := &fakeErrorTrimmer{}
	(&Housekeeper{Store: store, Errors: trimmer}).Tidy(context.Background(), now)

	day := 24 * time.Hour
	if want := now.Add(-2 * time.Hour); !store.rolledUpBefore.Equal(want) {
		t.Errorf("RollUp before %v, want %v", store.rolledUpBefore, want)
	}
	if !store.minutes.Equal(now.Add(-7*day)) || !store.hours.Equal(now.Add(-90*day)) || !store.runtimeTo.Equal(now.Add(-7*day)) {
		t.Errorf("DeleteBefore(%v, %v, %v), want 7, 90 and 7 days ago", store.minutes, store.hours, store.runtimeTo)
	}
	if trimmer.keep != 1000 {
		t.Errorf("Trim(%d), want Trim(1000)", trimmer.keep)
	}
}

func TestTidyTrimFailureStillTidiesMetrics(t *testing.T) {
	store := &fakeHousekeepingStore{}
	(&Housekeeper{Store: store, Errors: &fakeErrorTrimmer{err: errors.New("database down")}}).Tidy(context.Background(), time.Now())
	if !store.deleted {
		t.Error("DeleteBefore didn't run after Trim failed")
	}
}

func TestTidyKeepsMinutesWhenRollUpFails(t *testing.T) {
	store := &fakeHousekeepingStore{rollUpErr: errors.New("database down")}
	(&Housekeeper{Store: store, Errors: &fakeErrorTrimmer{}}).Tidy(context.Background(), time.Now())
	if store.deleted {
		t.Error("DeleteBefore ran after RollUp failed")
	}
}
