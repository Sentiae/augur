package usecase

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/sentiae/platform-kit/tenant"
)

type stubOrgLister struct {
	orgs []uuid.UUID
	err  error
}

func (s stubOrgLister) ListOrgIDs(ctx context.Context) ([]uuid.UUID, error) {
	return s.orgs, s.err
}

func TestForEachOrg(t *testing.T) {
	orgA := uuid.New()
	orgB := uuid.New()
	orgC := uuid.New()

	tests := []struct {
		name         string
		enforce      bool
		lister       stubOrgLister
		fnErrForOrg  map[uuid.UUID]error
		wantCalls    int
		wantStampErr bool // fn should observe a stamped org ctx when enforce is on
		wantErr      error
	}{
		{
			name:      "enforce off runs once on original ctx",
			enforce:   false,
			lister:    stubOrgLister{orgs: []uuid.UUID{orgA, orgB}},
			wantCalls: 1,
		},
		{
			name:      "enforce on runs once per org with stamped ctx",
			enforce:   true,
			lister:    stubOrgLister{orgs: []uuid.UUID{orgA, orgB, orgC}},
			wantCalls: 3,
		},
		{
			name:        "one org erroring does not abort the rest",
			enforce:     true,
			lister:      stubOrgLister{orgs: []uuid.UUID{orgA, orgB, orgC}},
			fnErrForOrg: map[uuid.UUID]error{orgB: errors.New("boom")},
			wantCalls:   3,
		},
		{
			name:    "lister error aborts and propagates",
			enforce: true,
			lister:  stubOrgLister{err: errors.New("list failed")},
			wantErr: errors.New("list failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.enforce {
				t.Setenv("APP_RLS_ENFORCE_ENABLED", "true")
			} else {
				os.Unsetenv("APP_RLS_ENFORCE_ENABLED")
			}

			var calls int
			var seenOrgs []uuid.UUID
			var stampedEvery = true

			err := ForEachOrg(context.Background(), tt.lister, func(orgCtx context.Context) error {
				calls++
				org, ok := tenant.SystemOrgFromContext(orgCtx)
				if tt.enforce {
					if !ok {
						stampedEvery = false
					} else {
						seenOrgs = append(seenOrgs, org)
					}
				} else if ok {
					// enforce-off must not stamp an org
					stampedEvery = false
				}
				if fnErr := tt.fnErrForOrg[org]; fnErr != nil {
					return fnErr
				}
				return nil
			})

			if tt.wantErr != nil {
				if err == nil || err.Error() != tt.wantErr.Error() {
					t.Fatalf("want err %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if calls != tt.wantCalls {
				t.Fatalf("want %d calls, got %d", tt.wantCalls, calls)
			}

			if tt.enforce {
				if !stampedEvery {
					t.Fatalf("enforce-on: every fn call must see a stamped org ctx")
				}
				if len(seenOrgs) != len(tt.lister.orgs) {
					t.Fatalf("want %d stamped orgs, got %d", len(tt.lister.orgs), len(seenOrgs))
				}
			} else {
				if !stampedEvery {
					t.Fatalf("enforce-off: fn must run on the original (unstamped) ctx")
				}
			}
		})
	}
}
