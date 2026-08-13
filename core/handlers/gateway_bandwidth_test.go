package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dylaris-core/services"
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

// rebalanceFakeStore embeds store.Store (nil) so it satisfies the full
// interface at compile time; only SetSetting is overridden, mirroring the
// dnsFakeStore pattern in dns_settings_test.go.
type rebalanceFakeStore struct {
	store.Store
	writes []struct{ key, value string }
	setErr error
}

func (f *rebalanceFakeStore) SetSetting(key, value string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.writes = append(f.writes, struct{ key, value string }{key, value})
	return nil
}

func rebalanceReq(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/api/gateway-bandwidth/rebalance", bytes.NewReader([]byte(body)))
}

func TestGatewayBandwidthSetRebalanceMode_Armed(t *testing.T) {
	fs := &rebalanceFakeStore{}
	h := NewGatewayBandwidthHandler(&AppState{Store: fs, FeatureFlags: services.NewFeatureFlags(nil)})

	rw := httptest.NewRecorder()
	h.SetRebalanceMode(rw, rebalanceReq(`{"mode":"armed"}`))

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rw.Code, rw.Body.String())
	}
	var out struct {
		Success bool   `json:"success"`
		Mode    string `json:"mode"`
	}
	if err := json.NewDecoder(rw.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Success || out.Mode != "armed" {
		t.Fatalf("unexpected body: %+v", out)
	}
	if len(fs.writes) != 1 || fs.writes[0].key != "warp_rebalance_mode" || fs.writes[0].value != "armed" {
		t.Fatalf("writes = %+v, want a single warp_rebalance_mode=armed write", fs.writes)
	}
}

func TestGatewayBandwidthSetRebalanceMode_RejectsUnknownMode(t *testing.T) {
	fs := &rebalanceFakeStore{}
	h := NewGatewayBandwidthHandler(&AppState{Store: fs, FeatureFlags: services.NewFeatureFlags(nil)})

	rw := httptest.NewRecorder()
	h.SetRebalanceMode(rw, rebalanceReq(`{"mode":"bogus"}`))

	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rw.Code, rw.Body.String())
	}
	if len(fs.writes) != 0 {
		t.Fatalf("rejected mode still wrote %+v", fs.writes)
	}
}

func TestGatewayBandwidthSetRebalanceMode_InvalidJSON(t *testing.T) {
	fs := &rebalanceFakeStore{}
	h := NewGatewayBandwidthHandler(&AppState{Store: fs, FeatureFlags: services.NewFeatureFlags(nil)})

	rw := httptest.NewRecorder()
	h.SetRebalanceMode(rw, rebalanceReq(`{not json`))

	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rw.Code, rw.Body.String())
	}
	if len(fs.writes) != 0 {
		t.Fatalf("malformed JSON still wrote %+v", fs.writes)
	}
}

func TestGatewayBandwidthSetRebalanceMode_StoreErrorIs500(t *testing.T) {
	fs := &rebalanceFakeStore{setErr: errors.New("db down")}
	h := NewGatewayBandwidthHandler(&AppState{Store: fs, FeatureFlags: services.NewFeatureFlags(nil)})

	rw := httptest.NewRecorder()
	h.SetRebalanceMode(rw, rebalanceReq(`{"mode":"off"}`))

	if rw.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rw.Code, rw.Body.String())
	}
}
