package caddy_geoip

import (
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/maxmind/geoipupdate/v4/pkg/geoipupdate"
	"github.com/maxmind/geoipupdate/v4/pkg/geoipupdate/database"

	"github.com/oschwald/maxminddb-golang"
	"go.uber.org/zap"
)

// Interface guards
var (
	_ caddy.Module          = (*GeoIP)(nil)
	_ caddy.Provisioner     = (*GeoIP)(nil)
	_ caddy.CleanerUpper    = (*GeoIP)(nil)
	_ caddyfile.Unmarshaler = (*GeoIP)(nil)
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

	mu     sync.Mutex
	dbInst *maxminddb.Reader
	done   chan bool
	logger *zap.Logger
}

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

func (GeoIP) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.geoip",
		New: func() caddy.Module { return new(GeoIP) },
	}
}

func (m *GeoIP) Provision(ctx caddy.Context) error {
	m.logger = ctx.Logger(m)

	m.done = make(chan bool, 1)

	// start the reload or the refresh timer
	if m.AccountID > 0 && m.APIKey != "" && m.DownloadFrequency > 0 {

		// download the database

		go func() {
			ticker := time.NewTicker(time.Duration(m.DownloadFrequency))
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					err := m.downloadDatabase()
					if err != nil {
						m.logger.Error("downloading database failed", zap.Error(err))
					}
				case <-m.done:
					m.logger.Info("downloading stopped")
					return
				}
			}
		}()

		return m.downloadDatabase()
	} else if m.ReloadFrequency > 0 {

		// start the reload frequency

		go func() {
			ticker := time.NewTicker(time.Duration(m.ReloadFrequency))
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					err := m.reloadDatabase()
					if err != nil {
						m.logger.Error("reload database failed", zap.Error(err))
					}
				case <-m.done:
					m.logger.Info("reloading stopped")
					return
				}
			}
		}()
	}

	// assume the database is local
	err := m.reloadDatabase()
	if err != nil {
		return fmt.Errorf("cannot open database file %s: %v", m.DbPath, err)
	}

	return nil
}

func (m *GeoIP) Cleanup() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// stop all background tasks
	if m.done != nil {
		close(m.done)
	}

	if m.dbInst != nil {
		return m.dbInst.Close()
	}

	return nil
}

func (m *GeoIP) downloadDatabase() error {
	m.logger.Info("reloading database")

	directoryPath, filename := filepath.Split(m.DbPath)

	edition := strings.Replace(filename, ".mmdb", "", 1)

	config := geoipupdate.Config{
		AccountID:         m.AccountID,
		DatabaseDirectory: directoryPath,
		LicenseKey:        m.APIKey,
		LockFile:          filepath.Join(directoryPath, ".geoipupdate.lock"),
		URL:               "https://updates.maxmind.com",
		EditionIDs:        []string{edition},
		Proxy:             nil,
		PreserveFileTimes: true,
		Verbose:           true,
		RetryFor:          5 * time.Minute,
	}

	m.logger.Info("starting download", zap.String("edition", edition))

	client := geoipupdate.NewClient(&config)
	dbReader := database.NewHTTPDatabaseReader(client, &config)
	editionID := edition

	dbWriter, err := database.NewLocalFileDatabaseWriter(m.DbPath, config.LockFile, config.Verbose)
	if err != nil {
		m.logger.Error("creating maxmind db writer", zap.Error(err))
	}

	if err := dbReader.Get(dbWriter, editionID); err != nil {
		m.logger.Error("getting database", zap.Error(err))
	}

	m.logger.Info("finished download", zap.String("edition", edition))

	return m.reloadDatabase()
}

func (m *GeoIP) reloadDatabase() error {
	m.logger.Info("reloading database")
	m.mu.Lock()
	defer m.mu.Unlock()

	newInstance, err := maxminddb.Open(m.DbPath)
	if err != nil {
		return err
	}

	// keep a reference to the old instance
	oldInstance := m.dbInst
	m.dbInst = newInstance

	if oldInstance != nil {
		m.logger.Info("closing old database")
		return oldInstance.Close()
	}

	m.logger.Info("reload successful",
		zap.Uint("epoch", m.dbInst.Metadata.BuildEpoch),
		zap.Uint("major", m.dbInst.Metadata.BinaryFormatMajorVersion),
		zap.Uint("minor", m.dbInst.Metadata.BinaryFormatMinorVersion))

	return nil
}

func (m *GeoIP) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {

	if m.TrustHeader != "" && r.Header.Get(m.TrustHeader) != "" {
		r.RemoteAddr = r.Header.Get(m.TrustHeader)
	}

	m.logger.Info("loading ip address", zap.String("remoteaddr", r.RemoteAddr))

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

	var record Record
	err = m.dbInst.Lookup(addr, &record)
	if err != nil {
		m.logger.Warn("cannot lookup IP address", zap.String("address", r.RemoteAddr), zap.Error(err))
		return err
	}

	repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
	repl.Set("geoip.country_code", record.Country.ISOCode)

	m.logger.Info(
		"found maxmind data",
		zap.String("ip", r.RemoteAddr),
		zap.String("country", record.Country.ISOCode),
	)

	return next.ServeHTTP(w, r)
}
