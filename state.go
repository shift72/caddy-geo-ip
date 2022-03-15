package caddy_geoip

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/maxmind/geoipupdate/v4/pkg/geoipupdate"
	"github.com/maxmind/geoipupdate/v4/pkg/geoipupdate/database"
	"github.com/oschwald/maxminddb-golang"
	"go.uber.org/zap"
)

type state struct {
	mu     sync.Mutex
	dbInst *maxminddb.Reader
	done   chan bool
	dbPath string

	config *geoipupdate.Config

	logger *zap.Logger
}

func (state *state) Provision(m *GeoIP) error {

	state.done = make(chan bool, 1)
	state.dbPath = m.DbPath

	// start the reload or the refresh timer
	if m.AccountID > 0 && m.APIKey != "" && m.DownloadFrequency > 0 {

		state.logger.Info("starting download ticker", zap.Duration("frequency", time.Duration(m.DownloadFrequency)))
		directoryPath, filename := filepath.Split(state.dbPath)

		edition := strings.Replace(filename, ".mmdb", "", 1)

		state.config = &geoipupdate.Config{
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

		// download the database

		go func() {
			ticker := time.NewTicker(time.Duration(m.DownloadFrequency))
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					err := state.downloadDatabase()
					if err != nil {
						state.logger.Error("downloading database failed", zap.Error(err))
					}
				case <-state.done:
					state.logger.Info("downloading stopped")
					return
				}
			}
		}()

		return state.downloadDatabase()
	} else if m.ReloadFrequency > 0 {

		// start the reload frequency

		state.logger.Info("starting reload ticker", zap.Duration("frequency", time.Duration(m.ReloadFrequency)))

		go func() {
			ticker := time.NewTicker(time.Duration(m.ReloadFrequency))
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					err := state.reloadDatabase()
					if err != nil {
						state.logger.Error("reload database failed", zap.Error(err))
					}
				case <-state.done:
					state.logger.Info("reloading stopped")
					return
				}
			}
		}()
	}

	// assume the database is local
	err := state.reloadDatabase()
	if err != nil {
		return fmt.Errorf("cannot open database file %s: %v", m.DbPath, err)
	}

	return nil
}

func (state *state) reloadDatabase() error {
	state.logger.Info("reloading database")
	state.mu.Lock()
	defer state.mu.Unlock()

	if _, err := os.Stat(state.dbPath); errors.Is(err, os.ErrNotExist) {
		state.logger.Warn("database does not exist", zap.String("dbpath", state.dbPath))
		return nil
	}

	newInstance, err := maxminddb.Open(state.dbPath)
	if err != nil {
		return err
	}

	// keep a reference to the old instance
	oldInstance := state.dbInst
	state.dbInst = newInstance

	if oldInstance != nil {
		state.logger.Info("closing old database")
		return oldInstance.Close()
	}

	state.logger.Info("reload successful",
		zap.Uint("epoch", state.dbInst.Metadata.BuildEpoch),
		zap.Uint("major", state.dbInst.Metadata.BinaryFormatMajorVersion),
		zap.Uint("minor", state.dbInst.Metadata.BinaryFormatMinorVersion))

	return nil
}

func (state *state) downloadDatabase() error {
	edition := state.config.EditionIDs[0]

	state.logger.Info("starting download", zap.String("edition", edition))

	client := geoipupdate.NewClient(state.config)
	dbReader := database.NewHTTPDatabaseReader(client, state.config)

	dbWriter, err := database.NewLocalFileDatabaseWriter(state.dbPath, state.config.LockFile, state.config.Verbose)
	if err != nil {
		state.logger.Error("creating maxmind db writer", zap.Error(err))
	}

	if err := dbReader.Get(dbWriter, edition); err != nil {
		state.logger.Error("getting database", zap.Error(err))
	}

	state.logger.Info("finished download", zap.String("edition", edition))

	return state.reloadDatabase()
}

func (state *state) logStatus() {
	if state.dbInst == nil {
		state.logger.Info("no geo database available")
	} else {
		state.logger.Debug("geo database available",
			zap.Uint("epoch", state.dbInst.Metadata.BuildEpoch),
			zap.Uint("major", state.dbInst.Metadata.BinaryFormatMajorVersion),
			zap.Uint("minor", state.dbInst.Metadata.BinaryFormatMinorVersion))
	}
}

func (state *state) Destruct() error {

	state.mu.Lock()
	defer state.mu.Unlock()

	// stop all background tasks
	if state.done != nil {
		close(state.done)
	}

	if state.dbInst != nil {
		return state.dbInst.Close()
	}

	return nil
}
