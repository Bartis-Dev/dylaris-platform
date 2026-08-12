package store

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// sqlmock's default matcher is regexp, so a metacharacter-free substring of the
// query matches without being fragile about the exact whitespace of the
// multi-line SQL. WithArgs still pins the parameters.
func TestGetGatewayBandwidthHistory_NoFilters(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	since := time.Unix(1730000000, 0)
	t0 := time.Unix(1730000030, 0)
	rows := sqlmock.NewRows([]string{"time", "component", "id", "host", "region", "rx_bps", "tx_bps", "cap_mbit"}).
		AddRow(t0, "warp", "eu-1", "web-eu-1", "eu-central", int64(100), int64(9000), 1000).
		AddRow(t0, "edge", "eu-a", "web-eu-1", "eu-central", int64(50), int64(200), 1000)

	mock.ExpectQuery("SELECT time, component, id, host, region, rx_bps, tx_bps, cap_mbit").
		WithArgs(since, "", "").
		WillReturnRows(rows)

	got, err := s.GetGatewayBandwidthHistory(since, "", "")
	if err != nil {
		t.Fatalf("GetGatewayBandwidthHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}
	if got[0].Component != "warp" || got[0].TxBps != 9000 || got[0].CapMbit != 1000 {
		t.Fatalf("unexpected first row: %+v", got[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestGetGatewayBandwidthHistory_HostFilterPassedThrough(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	since := time.Unix(1730000000, 0)
	mock.ExpectQuery("SELECT time, component, id, host, region, rx_bps, tx_bps, cap_mbit").
		WithArgs(since, "warp", "web-eu-1").
		WillReturnRows(sqlmock.NewRows([]string{"time", "component", "id", "host", "region", "rx_bps", "tx_bps", "cap_mbit"}))

	if _, err := s.GetGatewayBandwidthHistory(since, "warp", "web-eu-1"); err != nil {
		t.Fatalf("GetGatewayBandwidthHistory: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}
