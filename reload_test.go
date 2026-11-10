package caddy_geoip

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

func reloadTestDatabase(t *testing.T) string {
	t.Helper()
	// Synthetic Country MMDB containing only 127.0.0.1 -> FI, generated with
	// maxmind/mmdbwriter. Embedding it keeps this test independent of downloads.
	const fixture = "H4sIAAAAAAAC/13S13ITMRSAYWFM6L1D6L0kQOgdEohjO4bQAwGW9a7iyMQrs7sOcei9995jOoTwBlzwALwDueSKC16AHX6GTNBo5pujM9I50kiIbkKIdiFC0D3ghhBhoh5QBD2hF/SGPtAX+kF/GAADYRAMhiEwFIbBcBgBI2EUjIYxMBaKYRyMhwkwESbBZJgCU2EaTIcZMBNmwWyYA3OhBEphHsyHBVAGC2ERLIYlsBSWwXJYASthFayGNbAW1sF6KIcK2AAboRIiUAVRiEEcqiEBm2Az1MCWgJNCbCXaBtthB+yEXVALu2EP1MFe2Af7wYADYEIwRRIssEFCPaSgQQjFvjQchEbIgAMasnAIXPDAhxw0wWFohjy0wBE4CsfgOJzovNEXOAWn4QychXNwHi7ARbgEl+EKXIVrcB2Cx7xJ2VtwG+7AXbgH9+EBPIRH8BieBHzi6KdCPGPxObyAVijAS3gFr+ENvIV38B4+wEdo6/In/tJ1dEQsnXN8N99RpTxtWNqW5ZXRtm8/E2ZzQjl2qaUzP+qSyjHdvFGv3YzpGxkzrV2jSbqe0k4h9H9WOZ3Z1ngypxptQ2a11RAOpX/Vfk7Ypm8mTU8afj4rayJSVytflpVU0Efclp7lqqwf7P4eU9l/dYqijaaTypkp6YlwzAkaNf60/rU97kpLu7bhqRZZKP4NhAiKbJ0FAAA="
	compressed, err := base64.StdEncoding.DecodeString(fixture)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "country.mmdb")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReloadDatabaseConcurrentLookups(t *testing.T) {
	current := &state{dbPath: reloadTestDatabase(t), logger: zap.NewNop()}
	if err := current.reloadDatabase(); err != nil {
		t.Fatal(err)
	}
	handler := &GeoIP{state: current, logger: zap.NewNop()}
	next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		replacer := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
		country, ok := replacer.Get("geoip.country_code")
		if !ok || country != "FI" {
			t.Errorf("country = %v, want FI", country)
		}
		return nil
	})
	stop := make(chan struct{})
	var workers sync.WaitGroup
	defer func() {
		close(stop)
		workers.Wait()
		if err := current.Destruct(); err != nil {
			t.Error(err)
		}
	}()
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				current.logStatus()
				request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
				request.RemoteAddr = "127.0.0.1:12345"
				request = request.WithContext(context.WithValue(
					request.Context(), caddy.ReplacerCtxKey, caddy.NewReplacer()))
				if err := handler.ServeHTTP(httptest.NewRecorder(), request, next); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for i := 0; i < 1000; i++ {
		if err := current.reloadDatabase(); err != nil {
			t.Fatal(err)
		}
		current.logStatus()
	}
}

func TestReloadDoesNotWaitForNextHandler(t *testing.T) {
	for _, loaded := range []bool{false, true} {
		name := "missing reader"
		if loaded {
			name = "loaded reader"
		}
		t.Run(name, func(t *testing.T) {
			current := &state{dbPath: reloadTestDatabase(t), logger: zap.NewNop()}
			if loaded {
				if err := current.reloadDatabase(); err != nil {
					t.Fatal(err)
				}
			}
			handler := &GeoIP{state: current, logger: zap.NewNop()}
			entered := make(chan struct{})
			release := make(chan struct{})
			requestDone := make(chan error, 1)
			reloadDone := make(chan error, 1)
			next := caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error {
				close(entered)
				<-release
				return nil
			})
			request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
			request.RemoteAddr = "127.0.0.1:12345"
			request = request.WithContext(context.WithValue(
				request.Context(), caddy.ReplacerCtxKey, caddy.NewReplacer()))
			go func() { requestDone <- handler.ServeHTTP(httptest.NewRecorder(), request, next) }()
			<-entered
			go func() { reloadDone <- current.reloadDatabase() }()
			select {
			case err := <-reloadDone:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("reload blocked while the next handler was serving")
				close(release)
				<-requestDone
				<-reloadDone
				current.Destruct()
				return
			}
			close(release)
			if err := <-requestDone; err != nil {
				t.Error(err)
			}
			if err := current.Destruct(); err != nil {
				t.Error(err)
			}
		})
	}
}
