package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const fakeMetrics = `# HELP cloudflared_tunnel_total_requests x
# TYPE cloudflared_tunnel_total_requests counter
cloudflared_tunnel_total_requests 42
cloudflared_tunnel_request_errors 3
cloudflared_tunnel_concurrent_requests_per_tunnel 1
cloudflared_tunnel_ha_connections 4
cloudflared_tunnel_response_by_code{status_code="200"} 40
cloudflared_tunnel_response_by_code{status_code="404"} 2
cloudflared_tunnel_server_locations{connection_id="0",edge_location="sin01"} 1
cloudflared_tunnel_server_locations{connection_id="1",edge_location="old01"} 0
go_goroutines 12
`

func serve(t *testing.T, readyCode int, ready string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(readyCode)
		_, _ = w.Write([]byte(ready))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fakeMetrics))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

type payload struct {
	V                int    `json:"v"`
	OK               bool   `json:"ok"`
	ReadyStatus      int    `json:"readyStatus"`
	ReadyConnections int    `json:"readyConnections"`
	ConnectorID      string `json:"connectorId"`
	Tunnel           struct {
		TotalRequests      uint64            `json:"totalRequests"`
		RequestErrors      uint64            `json:"requestErrors"`
		ConcurrentRequests int64             `json:"concurrentRequests"`
		HAConnections      int               `json:"haConnections"`
		ResponseByCode     map[string]uint64 `json:"responseByCode"`
	} `json:"tunnel"`
	ServerLocations []string `json:"serverLocations"`
	Err             string   `json:"err"`
}

func run(t *testing.T, addr string) (payload, int) {
	t.Helper()
	s := collect(addr, 2*time.Second)
	b := marshal(s)
	if len(b) > 1024 {
		t.Fatalf("payload is %d bytes", len(b))
	}
	var p payload
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("not JSON: %v: %s", err, b)
	}
	code := 0
	if !s.ok {
		code = 1
	}
	return p, code
}

func TestReadyIs200(t *testing.T) {
	p, code := run(t, serve(t, 200, `{"status":200,"readyConnections":4,"connectorId":"abc-123"}`))
	if code != 0 || !p.OK || p.ReadyStatus != 200 || p.ReadyConnections != 4 || p.ConnectorID != "abc-123" {
		t.Fatalf("ready: %+v exit=%d", p, code)
	}
	if p.Tunnel.TotalRequests != 42 || p.Tunnel.RequestErrors != 3 || p.Tunnel.ConcurrentRequests != 1 || p.Tunnel.HAConnections != 4 {
		t.Fatalf("tunnel: %+v", p.Tunnel)
	}
	if p.Tunnel.ResponseByCode["200"] != 40 || p.Tunnel.ResponseByCode["404"] != 2 {
		t.Fatalf("by code: %+v", p.Tunnel.ResponseByCode)
	}
	if len(p.ServerLocations) != 1 || p.ServerLocations[0] != "sin01" {
		t.Fatalf("locations (only the current one): %v", p.ServerLocations)
	}
}

func TestReady503StillPrintsJSONAndExits1(t *testing.T) {
	p, code := run(t, serve(t, 503, `{"status":503,"readyConnections":0,"connectorId":"abc-123"}`))
	if code != 1 || p.OK || p.ReadyStatus != 503 || p.ReadyConnections != 0 || p.V != 1 {
		t.Fatalf("503: %+v exit=%d", p, code)
	}
}

func TestUnreachableIsAnErrorJSON(t *testing.T) {
	p, code := run(t, "127.0.0.1:1")
	if code != 1 || p.OK || !strings.HasPrefix(p.Err, "ready: ") || p.ReadyStatus != 0 {
		t.Fatalf("unreachable: %+v exit=%d", p, code)
	}
}
