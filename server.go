package mup

import (
	"labix.org/v2/mgo"
	"time"
)

type Config struct {
	// Database defines the MongoDB database that holds all data
	// for the mup instance that this mup server is part of.
	Database *mgo.Database

	// Refresh defines how often to refresh account and plugin
	// information from the database. Default to every few seconds.
	// Set to -1 to disable.
	Refresh time.Duration

	// Accounts defines which of the IRC accounts defined in the
	// database this server is responsible for. Defaults to all if nil.
	// Set to an empty list for handling no accounts in this server.
	Accounts []string

	// Plugins defines which of the plugins defined in the database
	// this server is responsible for. Defaults to all if nil. Set to
	// an empty list for handling no plugins in this server.
	Plugins []string
}

// A Server handles some or all of the duties of a mup instance.
//
// For any given mup plugin to work, a server must be configured
// to run the given plugin, and a server must be configured to run
// each of the IRC accounts the plugin is configured to handle.
//
// A simple mup deployment may run all of these tasks in a single
// server by not limiting which plugins and accounts the server
// is reponsible for. More complex deployments may run separate
// servers to dissociate restart times for in-development plugins,
// or for handling the communication with certain IRC accounts
// from within specific networks, for example.
//
// All servers of a mup instance must be configured to use the
// same MongoDB server and database.
//
type Server struct {
	accountManager *accountManager
	pluginManager  *pluginManager
}

// Start starts a mup server that handles some or all of the duties
// of a mup instance, as defined in the provided configuration.
func Start(config *Config) (*Server, error) {
	var st Server
	var err error
	configCopy := *config
	if configCopy.Refresh == 0 {
		configCopy.Refresh = 3 * time.Second
	}
	st.accountManager, err = startAccountManager(configCopy)
	if err != nil {
		return nil, err
	}
	st.pluginManager, err = startPluginManager(configCopy)
	if err != nil {
		st.accountManager.Stop()
		return nil, err
	}
	return &st, nil
}

// Stop synchronously terminates all activities of the mup server.
func (st *Server) Stop() error {
	err1 := st.pluginManager.Stop()
	err2 := st.accountManager.Stop()
	if err2 != nil {
		return err2
	}
	return err1
}

// RefreshAccounts reloads from the database all information about
// the IRC accounts this server is responsible for, and acts on any
// changes (joins/departs channels, changes nicks, etc).
//
// The server may be configured to do that regularly at defined
// intervals. See Config for details.
//
func (st *Server) RefreshAccounts() {
	st.accountManager.Refresh()
}

// RefreshPlugins reloads from the database all information about
// the plugins this server is responsible for.
func (st *Server) RefreshPlugins() {
	st.pluginManager.Refresh()
}
