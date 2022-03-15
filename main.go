package caddy_geoip

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	"go.uber.org/zap"
)

// Interface guards
var (
	_ caddy.Module          = (*GeoIP)(nil)
	_ caddy.Provisioner     = (*GeoIP)(nil)
	_ caddyfile.Unmarshaler = (*GeoIP)(nil)

	pool = caddy.NewUsagePool()
)

func init() {
	caddy.RegisterModule(GeoIP{})
	httpcaddyfile.RegisterHandlerDirective("geo_ip", parseCaddyfile)
}

// Allows finding the Country Code of an IP address using the Maxmind database
type GeoIP struct {

	// The AccountID of the maxmind account
	AccountID int `json:"account_id"`

	// The API Key used to download the latest file
	APIKey string `json:"api_key"`

	// The path of the MaxMind GeoLite2-Country.mmdb file.
	DbPath string `json:"db_path"`

	// The frequency to download a fresh version of the database file
	DownloadFrequency caddy.Duration `json:"download_frequency"`

	// The frequency to reload the database file
	ReloadFrequency caddy.Duration `json:"reload_frequency"`

	// The header to trust instead of the `RemoteAddr`
	TrustHeader string `json:"trust_header"`

	// The Country Code to set if no value could be found
	OverrideCountryCode string `json:"override_country_code"`

	logger *zap.Logger

	state *state
}

func (GeoIP) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.geoip",
		New: func() caddy.Module { return new(GeoIP) },
	}
}

func (m *GeoIP) Provision(ctx caddy.Context) error {
	m.logger = ctx.Logger(m)

	// load variables from env
	if os.Getenv("GEOIP_ACCOUNT_ID") != "" {
		i, err := strconv.Atoi(os.Getenv("GEOIP_ACCOUNT_ID"))
		if err != nil {
			return fmt.Errorf("reading account id: %w", err)
		}
		m.AccountID = i
	}

	if os.Getenv("GEOIP_API_KEY") != "" {
		m.APIKey = os.Getenv("GEOIP_API_KEY")
	}

	if os.Getenv("GEOIP_OVERRIDE_COUNTRY_CODE") != "" {
		m.OverrideCountryCode = os.Getenv("GEOIP_OVERRIDE_COUNTRY_CODE")
	}

	tmp, _, err := pool.LoadOrNew("geoip.state", func() (caddy.Destructor, error) {
		state := state{
			logger: ctx.Logger(m),
		}
		state.Provision(m)
		return &state, nil
	})
	if err != nil {
		m.logger.Error("unable to load previous state", zap.Error(err))
		return err
	}

	if state, ok := tmp.(*state); ok {
		m.state = state
		state.logStatus()
	}

	return nil
}

func (m *GeoIP) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	if m.TrustHeader != "" && r.Header.Get(m.TrustHeader) != "" {
		r.RemoteAddr = r.Header.Get(m.TrustHeader)
	}

	m.logger.Debug("loading ip address", zap.String("remoteaddr", r.RemoteAddr))

	remoteIp, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		m.logger.Warn("cannot split IP address", zap.String("address", r.RemoteAddr), zap.Error(err))
	}

	// Get the record from the database
	addr := net.ParseIP(remoteIp)
	if addr == nil {
		m.logger.Warn("cannot parse IP address", zap.String("address", r.RemoteAddr))
		return next.ServeHTTP(w, r)
	}

	if m.state.dbInst == nil {
		m.logger.Warn("no database loaded, skipping geoip lookup")

		repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
		repl.Set("geoip.country_code", "--")

		// local development - force the country code to a known value
		if m.OverrideCountryCode != "" {
			repl.Set("geoip.country_code", m.OverrideCountryCode)
		}

		return next.ServeHTTP(w, r)
	}

	var record Record
	err = m.state.dbInst.Lookup(addr, &record)
	if err != nil {
		m.logger.Warn("cannot lookup IP address", zap.String("address", r.RemoteAddr), zap.Error(err))
		return err
	}

	repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
	repl.Set("geoip.country_code", record.Country.ISOCode)

	m.logger.Debug(
		"found maxmind data",
		zap.String("ip", r.RemoteAddr),
		zap.String("country", record.Country.ISOCode),
	)

	// local development - force the country code to a known value
	if m.OverrideCountryCode != "" && record.Country.GeonameId == 0 {
		repl.Set("geoip.country_code", m.OverrideCountryCode)
	}

	return next.ServeHTTP(w, r)
}
