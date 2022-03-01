package caddy_geoip

import (
	"net/http"
	"testing"

	"github.com/caddyserver/caddy/v2/caddytest"
	"go.uber.org/zap"
)

func TestIPV4Json(t *testing.T) {
	tester := caddytest.NewTester(t)

	cfg := `{
		"apps": {
			"http": {
				"servers": {
					"srv0": {
						"listen": [
							":8080"
						],
						"routes": [
							{
								"handle": [
									{
										"handler": "geoip",
										"db_path": "GeoLite2-Country.mmdb",
										"trust_header": "X-Real-IP"
									},
									{
										"handler": "static_response",
										"status_code": 200,
										"body": "Hello from {geoip.country_code}"
									}
								]
							}
						]
					}
				}
			}
		}
	}
	`

	tester.InitServer(cfg, "json")

	req, err := http.NewRequest("GET", "http://geo.caddy.localhost:8080", nil)
	if err != nil {
		t.Fatalf("unable to create request %s", err)
	}

	req.Header.Add("X-Real-IP", "202.36.75.151:3000")
	tester.AssertResponse(req, 200, "Hello from NZ")
}

func TestIPV6Json(t *testing.T) {
	tester := caddytest.NewTester(t)

	cfg := `{
		"apps": {
			"http": {
				"servers": {
					"srv0": {
						"listen": [
							":8080"
						],
						"routes": [
							{
								"handle": [
									{
										"handler": "geoip",
										"db_path": "GeoLite2-Country.mmdb",
										"trust_header": "X-Real-IP"
									},
									{
										"handler": "static_response",
										"status_code": 200,
										"body": "Hello from {geoip.country_code}"
									}
								]
							}
						]
					}
				}
			}
		}
	}
	`

	tester.InitServer(cfg, "json")

	req, err := http.NewRequest("GET", "http://geo.caddy.localhost:8080", nil)
	if err != nil {
		t.Fatalf("unable to create request %s", err)
	}

	req.Header.Add("X-Real-IP", "[2400:bd00:43a8:0:5dfb:1f0c:863b:d246]:3000")

	tester.AssertResponse(req, 200, "Hello from NZ")
}

func TestIPV4Caddyfile(t *testing.T) {
	tester := caddytest.NewTester(t)

	cfg := `
		{
			http_port     8080
			https_port    8443
			order geo_ip first
		}

		localhost:8080 {

			geo_ip {
				account_id 				1000
				api_key 					REDACTED
				reload_frequency 	1d
			  db_path 					GeoLite2-Country.mmdb
				trust_header 			X-Real-IP
			}

			respond / 200 {
				body "Hello from {geoip.country_code}"
			}
		}
	`

	tester.InitServer(cfg, "caddyfile")

	req, err := http.NewRequest("GET", "http://localhost:8080", nil)
	if err != nil {
		t.Fatalf("unable to create request %s", err)
	}

	req.Header.Add("X-Real-IP", "202.36.75.151:3000")
	tester.AssertResponse(req, 200, "Hello from NZ")
}

func xTestDownload(t *testing.T) {

	logger := zap.NewExample()

	g := GeoIP{
		logger:    logger,
		AccountID: 0,
		APIKey:    "",
		DbPath:    "GeoLite2-Country.mmdb",
	}

	err := g.downloadDatabase()

	if err != nil {
		t.Fail()
	}
}
