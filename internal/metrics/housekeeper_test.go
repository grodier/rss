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

type fakeRUMStore struct {
	rollUpErr      error
	rolledUpBefore time.Time
	deleted        bool
	views, daily   time.Time
}

func (f *fakeRUMStore) RollUp(ctx context.Context, before time.Time) error {
	f.rolledUpBefore = before
	return f.rollUpErr
}

func (f *fakeRUMStore) DeleteBefore(ctx context.Context, views, daily time.Time) error {
	f.deleted = true
	f.views, f.daily = views, daily
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
	rum := &fakeRUMStore{}
	trimmer := &fakeErrorTrimmer{}
	(&Housekeeper{Store: store, RUM: rum, Errors: trimmer}).Tidy(context.Background(), now)

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
	if want := now.Add(-time.Hour); !rum.rolledUpBefore.Equal(want) {
		t.Errorf("RUM RollUp before %v, want %v", rum.rolledUpBefore, want)
	}
	if want := time.Date(2025, 5, 1, 12, 30, 0, 0, time.UTC); !rum.views.Equal(now.Add(-30*day)) || !rum.daily.Equal(want) {
		t.Errorf("RUM DeleteBefore(%v, %v), want 30 days and 13 months ago", rum.views, rum.daily)
	}
}

func TestTidyTrimFailureStillTidiesMetrics(t *testing.T) {
	store := &fakeHousekeepingStore{}
	rum := &fakeRUMStore{}
	(&Housekeeper{Store: store, RUM: rum, Errors: &fakeErrorTrimmer{err: errors.New("database down")}}).Tidy(context.Background(), time.Now())
	if !store.deleted || !rum.deleted {
		t.Errorf("after Trim failed, DeleteBefore ran for requests: %t, for real-user views: %t; want both", store.deleted, rum.deleted)
	}
}

func TestTidyKeepsMinutesWhenRollUpFails(t *testing.T) {
	store := &fakeHousekeepingStore{rollUpErr: errors.New("database down")}
	rum := &fakeRUMStore{}
	(&Housekeeper{Store: store, RUM: rum, Errors: &fakeErrorTrimmer{}}).Tidy(context.Background(), time.Now())
	if store.deleted {
		t.Error("DeleteBefore ran after RollUp failed")
	}
	if !rum.deleted {
		t.Error("real-user views weren't tidied after the request metrics' RollUp failed")
	}
}

func TestTidyKeepsViewsWhenRUMRollUpFails(t *testing.T) {
	store := &fakeHousekeepingStore{}
	rum := &fakeRUMStore{rollUpErr: errors.New("database down")}
	(&Housekeeper{Store: store, RUM: rum, Errors: &fakeErrorTrimmer{}}).Tidy(context.Background(), time.Now())
	if rum.deleted {
		t.Error("RUM DeleteBefore ran after its RollUp failed")
	}
	if !store.deleted {
		t.Error("request metrics weren't tidied after the real-user views' RollUp failed")
	}
}
