// Package plugins imports all standard mup plugins so they register themselves.
package plugins

import (
	_ "gopkg.in/mup.v0/plugins/aql"
	_ "gopkg.in/mup.v0/plugins/echo"
	_ "gopkg.in/mup.v0/plugins/help"
	_ "gopkg.in/mup.v0/plugins/launchpad"
	_ "gopkg.in/mup.v0/plugins/ldap"
	_ "gopkg.in/mup.v0/plugins/log"
	_ "gopkg.in/mup.v0/plugins/publishbot"
	_ "gopkg.in/mup.v0/plugins/sendraw"
	_ "gopkg.in/mup.v0/plugins/wolframalpha"
)
