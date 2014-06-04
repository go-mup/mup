package mup

import (
	"labix.org/v2/mgo"
	"time"
)

type Config struct {
	// Database defines the MongoDB database that holds all of the
	// data for all stations responsible for the mup instance this
	// station is part of.
	Database *mgo.Database

	// Refresh defines how often to refresh server and plugin
	// information from the database. Default to every few seconds.
	// Set to -1 to disable.
	Refresh  time.Duration

	// Servers defines which of the IRC servers defined in the database
	// this station is responsible for. Defaults to all if nil. Set to
	// an empty list for handling no servers in this station.
	Servers  []string

	// Plugins defines which of the plugins defined in the database
	// this station is responsible for. Defaults to all if nil. Set to
	// an empty list for handling no plugins in this station.
	Plugins  []string
}

// A Station handles some or all of the duties of a mup instance.
//
// For any given mup plugin to work, a station must be configured
// to run the given plugin, and a station must be configured
// to run each of the servers the plugin is configured to
// communicate with.
//
// A simple mup deployment may run all of these tasks in a single
// station by not limiting which plugins and servers the station
// is reponsible for. More complex deployments may run separate
// stations to dissociate restart times for in-development plugins,
// or for handling the communication with certain IRC servers from
// within specific networks, for example.
//
// All stations of a mup instance must be configured to use the
// same MongoDB server and database.
//
type Station struct {
	bridge   *bridge
	pmanager *pluginManager
}

// Start starts a mup station that handles some or all of the duties
// of a mup instance, as defined in the provided configuration.
func Start(config *Config) (*Station, error) {
	var st Station
	var err error
	configCopy := *config
	if configCopy.Refresh == 0 {
		configCopy.Refresh = 3 * time.Second
	}
	st.bridge, err = startBridge(configCopy)
	if err != nil {
		return nil, err
	}
	st.pmanager, err = startPluginManager(configCopy)
	if err != nil {
		st.bridge.Stop()
		return nil, err
	}
	return &st, nil
}

// Stop synchronously terminates all activities of the mup station.
func (st *Station) Stop() error {
	err1 := st.pmanager.Stop()
	err2 := st.bridge.Stop()
	if err2 != nil {
		return err2
	}
	return err1
}

// RefreshServer reloads from the database all information about
// the IRC servers this station is responsible for.
func (st *Station) RefreshServers() {
	st.bridge.Refresh()
}
