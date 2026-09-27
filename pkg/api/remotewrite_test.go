package api

import (
	"bytes"
	"math"
	"net/http"
	"net/url"
	"testing"

	"github.com/klauspost/compress/snappy"
	"google.golang.org/protobuf/encoding/protowire"
)

func msg(b []byte, num protowire.Number, payload []byte) []byte {
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendBytes(b, payload)
}

func writeRequest(name, job string, t int64, v float64) []byte {
	label := func(n, v string) []byte {
		return msg(msg(nil, 1, []byte(n)), 2, []byte(v))
	}
	sample := protowire.AppendTag(nil, 1, protowire.Fixed64Type)
	sample = protowire.AppendFixed64(sample, math.Float64bits(v))
	sample = protowire.AppendTag(sample, 2, protowire.VarintType)
	sample = protowire.AppendVarint(sample, uint64(t))
	ts := msg(nil, 1, label("__name__", name))
	ts = msg(ts, 1, label("job", job))
	ts = msg(ts, 2, sample)
	md := protowire.AppendTag(nil, 1, protowire.VarintType)
	md = protowire.AppendVarint(md, 1) // counter
	md = msg(md, 2, []byte(name))
	md = msg(md, 4, []byte("Jobs pushed."))
	return msg(msg(nil, 1, ts), 3, md)
}

func TestRemoteWrite(t *testing.T) {
	srv := setup(t)
	post := func(body []byte) int {
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/write", bytes.NewReader(snappy.Encode(nil, body)))
		req.Header.Set("Content-Type", "application/x-protobuf")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(writeRequest("pushed_total", "batch", 590_000, 7)); code != http.StatusNoContent {
		t.Fatalf("status %d", code)
	}
	_, out := get(t, srv, "/api/v1/query", url.Values{"query": {`pushed_total{job="batch"}`}})
	res := out["data"].(map[string]any)["result"].([]any)
	if len(res) != 1 || res[0].(map[string]any)["value"].([]any)[1] != "7" {
		t.Fatalf("result = %v", res)
	}
	_, out = get(t, srv, "/api/v1/metadata", url.Values{"metric": {"pushed_total"}})
	if md := out["data"].(map[string]any)["pushed_total"]; md == nil {
		t.Fatalf("metadata = %v", out)
	}
	// An older sample for the same series is permanently rejected: 400, no retry.
	if code := post(writeRequest("pushed_total", "batch", 1_000, 1)); code != http.StatusBadRequest {
		t.Fatalf("out of order: status %d", code)
	}
	if code := post([]byte("not snappy")); code != http.StatusBadRequest {
		t.Fatalf("garbage: status %d", code)
	}
}
