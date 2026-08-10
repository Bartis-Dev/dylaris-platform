package store

import (
	"dylaris-core/models"
	"testing"
	"time"
)

func TestGatewayBandwidthRowFields(t *testing.T) {
	r := models.GatewayBandwidthRow{
		Time: time.Unix(1730000000, 0), Component: "warp", ID: "eu-1",
		Host: "web-eu-1", Region: "eu-central", RxBps: 100, TxBps: 200, CapMbit: 1000,
	}
	if r.Component != "warp" || r.RxBps != 100 || r.CapMbit != 1000 {
		t.Fatalf("unexpected row: %+v", r)
	}
}
