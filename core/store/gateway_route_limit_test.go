package store

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// A negative cap is the pre-nullable spelling of "unlimited". Rows written then
// are still in the table, and every enforcement site tests "count >= limit" -
// which a negative satisfies with zero routes held. The value that meant NO
// LIMIT therefore denies everything, and the refusal quotes it back at the
// operator: "You have used all -1 addresses on our domains".
func TestGetGatewayRouteLimit_NegativeReadsAsNoCap(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	mock.ExpectQuery("gateway_route_limits").
		WithArgs("global").
		WillReturnRows(sqlmock.NewRows([]string{"id", "scope", "max_routes"}).AddRow(1, "global", -1))

	l, err := s.GetGatewayRouteLimit("global")
	if err != nil {
		t.Fatalf("GetGatewayRouteLimit: %v", err)
	}
	if l == nil {
		t.Fatal("the ROW still exists; only its cap is nil")
	}
	if l.MaxRoutes != nil {
		t.Errorf("MaxRoutes = %d, want nil (no cap)", *l.MaxRoutes)
	}
}

// The two values that ARE part of the convention must survive untouched - 0
// especially, since it is the one an operator sets to hand out nothing.
func TestGetGatewayRouteLimit_KeepsZeroAndPositive(t *testing.T) {
	cases := []struct {
		name   string
		stored int
	}{
		{"zero means none", 0},
		{"a real cap", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			defer db.Close()
			s := NewPostgresStore(db)

			mock.ExpectQuery("gateway_route_limits").
				WithArgs("user:u1").
				WillReturnRows(sqlmock.NewRows([]string{"id", "scope", "max_routes"}).AddRow(1, "user:u1", tc.stored))

			l, err := s.GetGatewayRouteLimit("user:u1")
			if err != nil {
				t.Fatalf("GetGatewayRouteLimit: %v", err)
			}
			if l.MaxRoutes == nil || *l.MaxRoutes != tc.stored {
				t.Errorf("MaxRoutes = %v, want %d", l.MaxRoutes, tc.stored)
			}
		})
	}
}

// Healing on read alone would leave the bad row in place, so the next save of
// the same screen puts it straight back.
func TestSetGatewayRouteLimit_NegativeStoresNull(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	mock.ExpectExec("gateway_route_limits").
		WithArgs("global", nil).
		WillReturnResult(sqlmock.NewResult(1, 1))

	n := -1
	if err := s.SetGatewayRouteLimit("global", &n); err != nil {
		t.Fatalf("SetGatewayRouteLimit: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet: %v", err)
	}
}
