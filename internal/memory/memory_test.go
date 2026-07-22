package memory

import (
	"context"
	"strings"
	"testing"
)

func TestSaveCreatesFileAndIndex(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	out, err := NewSave(st).Call(context.Background(),
		`{"topic":"Server IPs","content":"swarmery-server LAN: 192.168.1.50"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "server-ips.md") {
		t.Fatalf("out = %q", out)
	}
	idx := st.Index(4000)
	if !strings.Contains(idx, "server-ips.md") || !strings.Contains(idx, "Server IPs") {
		t.Fatalf("index = %q", idx)
	}
}

func TestSaveOverwritesSameTopicWithoutDuplicateIndex(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	s := NewSave(st)
	s.Call(context.Background(), `{"topic":"Pi-hole","content":"v1"}`)
	s.Call(context.Background(), `{"topic":"Pi-hole","content":"v2"}`)
	if n := strings.Count(st.Index(4000), "pi-hole.md"); n != 1 {
		t.Fatalf("index entries = %d", n)
	}
	got, _ := NewSearch(st).Call(context.Background(), `{"query":"v2"}`)
	if !strings.Contains(got, "v2") {
		t.Fatalf("search = %q", got)
	}
}

func TestSearchFindsLinesCaseInsensitive(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	NewSave(st).Call(context.Background(), `{"topic":"Router","content":"Admin URL http://192.168.1.1"}`)
	out, err := NewSearch(st).Call(context.Background(), `{"query":"ADMIN url"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "192.168.1.1") || !strings.Contains(out, "router.md") {
		t.Fatalf("out = %q", out)
	}
}

func TestSearchNoResults(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	out, err := NewSearch(st).Call(context.Background(), `{"query":"nothing"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No memory entries") {
		t.Fatalf("out = %q", out)
	}
}

func TestIndexEmptyWhenNoMemory(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	if got := st.Index(4000); got != "" {
		t.Fatalf("index = %q", got)
	}
}
