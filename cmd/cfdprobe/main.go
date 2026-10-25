// Command cfdprobe is the HEALTHCHECK program of the cloudflared container:
// it reads cloudflared's own metrics listener on loopback,
// prints ONE JSON line and exits 0 exactly when /ready answers 200, so Docker's
// health IS cloudflared's readiness and State.Health.Log[-1].Output carries a
// few tunnel counters for a reader that never joins the container's network
// and never execs: it only reads `docker inspect`.
//
// It is built static (CGO off) and copied into a derived cloudflared image,
// because that image is distroless: no shell, no curl. Raw net.Dial and
// HTTP/1.0 keep the binary small (no net/http, no encoding/json).
//
//	cfdprobe [addr]       addr defaults to 127.0.0.1:20241
package main

import (
	"bytes"
	"net"
	"os"
	"strconv"
	"time"
)

const (
	defaultAddr = "127.0.0.1:20241"
	timeout     = 2 * time.Second
	maxReady    = 4 << 10
	maxMetrics  = 512 << 10
	maxCodes    = 32
	maxLocs     = 16
)

// Series names of cloudflared 2026.9.3, checked against a connected tunnel.
const (
	mTotal  = "cloudflared_tunnel_total_requests"
	mErrors = "cloudflared_tunnel_request_errors"
	mConc   = "cloudflared_tunnel_concurrent_requests_per_tunnel"
	mHA     = "cloudflared_tunnel_ha_connections"
	mCodes  = "cloudflared_tunnel_response_by_code"
	mLocs   = "cloudflared_tunnel_server_locations"
)

type code struct {
	c string
	n uint64
}

// status is the payload (about 500 bytes): v seq ts ok readyStatus
// readyConnections connectorId tunnel{...} serverLocations err.
type status struct {
	seq         uint64
	ts          string
	ok          bool
	readyStatus int
	ready       int
	connectorID string
	total, errs uint64
	conc        int64
	ha          int
	byCode      []code
	locations   []string
	err         string
}

func main() {
	addr := defaultAddr
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}
	s := collect(addr, timeout)
	s.seq = uint64(time.Now().Unix())
	os.Stdout.Write(append(marshal(s), '\n'))
	if !s.ok {
		os.Exit(1)
	}
}

// collect reads /ready, then /metrics, within one deadline. ok means /ready
// answered 200 and /metrics was read; err is set only when a transport step
// failed. A non-200 /ready is not an error here: its JSON is still printed.
func collect(addr string, d time.Duration) *status {
	s := &status{ts: time.Now().UTC().Format(time.RFC3339Nano)}
	deadline := time.Now().Add(d)
	rb, c, err := get(addr, "/ready", deadline, maxReady)
	s.readyStatus = c
	if err != nil {
		s.err = "ready: " + err.Error()
		return s
	}
	s.ready = jsonInt(rb, "readyConnections")
	s.connectorID = jsonStr(rb, "connectorId")
	mb, mc, err := get(addr, "/metrics", deadline, maxMetrics)
	if err != nil {
		s.err = "metrics: " + err.Error()
		return s
	}
	if mc != 200 {
		s.err = "metrics: http " + strconv.Itoa(mc)
		return s
	}
	parseMetrics(mb, s)
	s.ok = c == 200
	return s
}

type perr string

func (e perr) Error() string { return string(e) }

const (
	errTooBig = perr("response too big")
	errBad    = perr("bad http response")
)

func get(addr, path string, deadline time.Time, max int) ([]byte, int, error) {
	d := time.Until(deadline)
	if d <= 0 {
		d = time.Millisecond
	}
	c, err := net.DialTimeout("tcp", addr, d)
	if err != nil {
		return nil, 0, err
	}
	defer c.Close()
	_ = c.SetDeadline(deadline)
	if _, err = c.Write([]byte("GET " + path + " HTTP/1.0\r\nHost: " + addr + "\r\nConnection: close\r\n\r\n")); err != nil {
		return nil, 0, err
	}
	buf := make([]byte, 0, 20000)
	tmp := make([]byte, 8192)
	for {
		n, err := c.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if len(buf) > max {
			return nil, 0, errTooBig
		}
		if err != nil {
			if len(buf) == 0 {
				return nil, 0, err
			}
			break // EOF (Connection: close) or a deadline after data: parse what came
		}
	}
	i := bytes.Index(buf, []byte("\r\n\r\n"))
	if i < 0 || len(buf) < 12 {
		return nil, 0, errBad
	}
	n, _ := strconv.Atoi(string(buf[9:12]))
	return buf[i+4:], n, nil
}

func jsonInt(b []byte, key string) int {
	i := bytes.Index(b, []byte(`"`+key+`":`))
	if i < 0 {
		return 0
	}
	i += len(key) + 3
	j := i
	for j < len(b) && b[j] >= '0' && b[j] <= '9' {
		j++
	}
	n, _ := strconv.Atoi(string(b[i:j]))
	return n
}

func jsonStr(b []byte, key string) string {
	i := bytes.Index(b, []byte(`"`+key+`":"`))
	if i < 0 {
		return ""
	}
	i += len(key) + 4
	j := bytes.IndexByte(b[i:], '"')
	if j < 0 || j > 64 {
		return ""
	}
	return string(b[i : i+j])
}

func pf(v []byte) float64 {
	f, _ := strconv.ParseFloat(string(bytes.TrimSpace(v)), 64)
	if f < 0 {
		return 0
	}
	return f
}

func label(line []byte, key string) string {
	i := bytes.Index(line, []byte(key+`="`))
	if i < 0 {
		return ""
	}
	i += len(key) + 2
	j := bytes.IndexByte(line[i:], '"')
	if j < 0 || j > 32 {
		return ""
	}
	return string(line[i : i+j])
}

// parseMetrics keeps only the chosen series of the Prometheus text format.
func parseMetrics(body []byte, s *status) {
	for len(body) > 0 {
		var line []byte
		if i := bytes.IndexByte(body, '\n'); i >= 0 {
			line, body = body[:i], body[i+1:]
		} else {
			line, body = body, nil
		}
		if len(line) < 20 || line[0] != 'c' {
			continue
		}
		sp := bytes.LastIndexByte(line, ' ')
		if sp < 0 {
			continue
		}
		val := line[sp+1:]
		switch {
		case bytes.HasPrefix(line, []byte(mTotal+" ")):
			s.total = uint64(pf(val))
		case bytes.HasPrefix(line, []byte(mErrors+" ")):
			s.errs = uint64(pf(val))
		case bytes.HasPrefix(line, []byte(mConc+" ")):
			s.conc = int64(pf(val))
		case bytes.HasPrefix(line, []byte(mHA+" ")):
			s.ha = int(pf(val))
		case bytes.HasPrefix(line, []byte(mCodes+"{")):
			if c := label(line, "status_code"); c != "" && len(s.byCode) < maxCodes {
				s.byCode = append(s.byCode, code{c, uint64(pf(val))})
			}
		case bytes.HasPrefix(line, []byte(mLocs+"{")):
			// 1 = where the connection is now, 0 = a previous location.
			if l := label(line, "edge_location"); l != "" && pf(val) >= 1 && len(s.locations) < maxLocs {
				s.locations = append(s.locations, l)
			}
		}
	}
}

func appendStr(b []byte, s string) []byte {
	b = append(b, '"')
	for i := range len(s) {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			b = append(b, '\\', c)
		case c < 0x20 || c >= 0x7f:
			b = append(b, '?')
		default:
			b = append(b, c)
		}
	}
	return append(b, '"')
}

// marshal renders the payload by hand, in a fixed field order.
func marshal(s *status) []byte {
	b := make([]byte, 0, 700)
	b = append(b, `{"v":1,"seq":`...)
	b = strconv.AppendUint(b, s.seq, 10)
	b = append(b, `,"ts":`...)
	b = appendStr(b, s.ts)
	b = append(b, `,"ok":`...)
	b = strconv.AppendBool(b, s.ok)
	b = append(b, `,"readyStatus":`...)
	b = strconv.AppendInt(b, int64(s.readyStatus), 10)
	b = append(b, `,"readyConnections":`...)
	b = strconv.AppendInt(b, int64(s.ready), 10)
	b = append(b, `,"connectorId":`...)
	b = appendStr(b, s.connectorID)
	b = append(b, `,"tunnel":{"totalRequests":`...)
	b = strconv.AppendUint(b, s.total, 10)
	b = append(b, `,"requestErrors":`...)
	b = strconv.AppendUint(b, s.errs, 10)
	b = append(b, `,"concurrentRequests":`...)
	b = strconv.AppendInt(b, s.conc, 10)
	b = append(b, `,"haConnections":`...)
	b = strconv.AppendInt(b, int64(s.ha), 10)
	b = append(b, `,"responseByCode":{`...)
	for i, c := range s.byCode {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendStr(b, c.c)
		b = append(b, ':')
		b = strconv.AppendUint(b, c.n, 10)
	}
	b = append(b, `}},"serverLocations":[`...)
	for i, l := range s.locations {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendStr(b, l)
	}
	b = append(b, `],"err":`...)
	if len(s.err) > 160 {
		s.err = s.err[:160]
	}
	b = appendStr(b, s.err)
	return append(b, '}')
}
