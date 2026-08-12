package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dylaris-core/store"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGatewayBandwidthGetHistory_ShapeAndRange(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	st := store.NewPostgresStore(db)

	t0 := time.Unix(1730000000, 0)
	mock.ExpectQuery("SELECT time, component, id, host, region, rx_bps, tx_bps, cap_mbit").
		WillReturnRows(sqlmock.NewRows([]string{"time", "component", "id", "host", "region", "rx_bps", "tx_bps", "cap_mbit"}).
			AddRow(t0, "warp", "eu-1", "web-eu-1", "eu", int64(100), int64(200), 1000))

	h := NewGatewayBandwidthHandler(&AppState{Store: st})
	req := httptest.NewRequest(http.MethodGet, "/api/gateway-bandwidth/history?range=6h&host=web-eu-1", nil)
	rw := httptest.NewRecorder()
	h.GetHistory(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	var body struct {
		Points []struct {
			TS    int64  `json:"ts"`
			RxBps uint64 `json:"rxBps"`
			TxBps uint64 `json:"txBps"`
		} `json:"points"`
	}
	if err := json.NewDecoder(rw.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Points) != 1 || body.Points[0].TS != t0.Unix() || body.Points[0].TxBps != 200 {
		t.Fatalf("unexpected points: %+v", body.Points)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestGatewayBandwidthGetHistory_NoStore(t *testing.T) {
	h := NewGatewayBandwidthHandler(&AppState{})
	rw := httptest.NewRecorder()
	h.GetHistory(rw, httptest.NewRequest(http.MethodGet, "/api/gateway-bandwidth/history", nil))
	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rw.Code)
	}
}
