package caddy_geoip

import (
	"strconv"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func (m *GeoIP) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {

	d.NextArg()

	for d.NextBlock(0) {
		var err error

		switch d.Val() {
		case "db_path":
			if !d.Args(&m.DbPath) {
				return d.Errf("Missing db path")
			}

		case "trust_header":
			d.Args(&m.TrustHeader)

		case "account_id":
			var val string
			if d.Args(&val) {
				accountID, err := strconv.Atoi(val)
				if err != nil {
					return d.Errf("invalid account number %s: %v", d.Val(), err)
				}
				m.AccountID = accountID
			}

		case "api_key":
			d.Args(&m.APIKey)

		case "reload_frequency":
			if !d.NextArg() {
				return d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return d.Errf("bad duration value %s: %v", d.Val(), err)
			}
			m.ReloadFrequency = caddy.Duration(dur)

		case "download_frequency":
			if !d.NextArg() {
				return d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return d.Errf("bad duration value %s: %v", d.Val(), err)
			}
			m.DownloadFrequency = caddy.Duration(dur)

		case "override_country_code":
			d.Args(&m.OverrideCountryCode)

		}
		if err != nil {
			return d.Errf("Error parsing %s: %s", d.Val(), err)
		}
	}
	return nil
}

// parseCaddyfile unmarshal tokens from h into a new Middleware.
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var m GeoIP
	err := m.UnmarshalCaddyfile(h.Dispenser)
	return &m, err
}
