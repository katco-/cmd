package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	. "launchpad.net/gocheck"
	"launchpad.net/juju-core/environs"
	"launchpad.net/juju-core/environs/config"
	"launchpad.net/juju-core/environs/dummy"
	"launchpad.net/juju-core/juju/testing"
	"launchpad.net/juju-core/state"
	coretesting "launchpad.net/juju-core/testing"
	"launchpad.net/juju-core/version"
	"net/http"
	"path/filepath"
	"time"
)

var _ = Suite(&upgraderSuite{})

type upgraderSuite struct {
	oldVersion version.Binary
	testing.JujuConnSuite
}

func (s *upgraderSuite) SetUpTest(c *C) {
	s.JujuConnSuite.SetUpTest(c)
	s.oldVersion = version.Current
}

func (s *upgraderSuite) TearDownTest(c *C) {
	version.Current = s.oldVersion
	s.JujuConnSuite.TearDownTest(c)
}

func (s *upgraderSuite) TestUpgraderError(c *C) {
	st, err := state.Open(s.StateInfo(c))
	c.Assert(err, IsNil)
	// We have no installed tools, so the logic should set the agent
	// tools anyway, but with no URL.
	u := startUpgrader(c, st, c.MkDir(), &state.Tools{Binary: version.Current})

	// Close the state under the watcher and check that the upgrader dies.
	st.Close()
	waitDeath(c, u, nil, "watcher: cannot get content of node.*")
}

func (s *upgraderSuite) TestUpgraderStop(c *C) {
	u := startUpgrader(c, s.State, c.MkDir(), &state.Tools{Binary: version.Current})
	err := u.Stop()
	c.Assert(err, IsNil)
}

func (s *upgraderSuite) proposeVersion(c *C, vers version.Number, development bool) {
	cfg, err := s.State.EnvironConfig()
	c.Assert(err, IsNil)
	attrs := cfg.AllAttrs()
	attrs["agent-version"] = vers.String()
	attrs["development"] = development
	newCfg, err := config.New(attrs)
	c.Assert(err, IsNil)
	err = s.State.SetEnvironConfig(newCfg)
	c.Assert(err, IsNil)
}

func (s *upgraderSuite) uploadTools(c *C, vers version.Binary) *state.Tools {
	tgz := coretesting.TarGz(
		coretesting.NewTarFile("juju", 0777, "juju contents "+vers.String()),
		coretesting.NewTarFile("jujuc", 0777, "jujuc contents "+vers.String()),
		coretesting.NewTarFile("jujud", 0777, "jujud contents "+vers.String()),
	)
	storage := s.Conn.Environ.Storage()
	err := storage.Put(environs.ToolsStoragePath(vers), bytes.NewReader(tgz), int64(len(tgz)))
	c.Assert(err, IsNil)
	url, err := s.Conn.Environ.Storage().URL(environs.ToolsStoragePath(vers))
	c.Assert(err, IsNil)
	return &state.Tools{URL: url, Binary: vers}
}

type proposal struct {
	version    string
	devVersion bool
}

var upgraderTests = []struct {
	about      string
	current    string   // current version.
	upload     []string // Upload these tools versions.
	propose    string   // Propose this version...
	devVersion bool     // ... with devVersion set to this.

	// upgradeTo is blank if nothing should happen.
	upgradeTo string
}{{
	about:   "propose with no possible candidates",
	propose: "2.0.2",
}, {
	about:   "propose with same candidate as current",
	upload:  []string{"2.0.0"},
	propose: "2.0.4",
}, {
	about:   "propose development version when !devVersion",
	upload:  []string{"2.0.1"},
	propose: "2.0.4",
}, {
	about:      "propose development version when devVersion",
	propose:    "2.0.4",
	devVersion: true,
	upgradeTo:  "2.0.1",
}, {
	about:     "propose release version when !devVersion",
	propose:   "2.0.4",
	upgradeTo: "2.0.0",
}, {
	about:   "propose with higher available candidates",
	upload:  []string{"2.0.5", "2.0.6"},
	propose: "2.0.4",
}, {
	about:     "propose exact available version",
	propose:   "2.0.6",
	upgradeTo: "2.0.6",
}, {
	about:     "propose downgrade",
	propose:   "2.0.5",
	upgradeTo: "2.0.5",
}, {
	about:     "upgrade with no proposal",
	current:   "2.0.6-foo-bar",
	upgradeTo: "2.0.5",
},
}

func (s *upgraderSuite) TestUpgrader(c *C) {
	dataDir, currentTools := s.primeTools(c, version.MustParseBinary("2.0.0-foo-bar"))
	// Remove the tools from the storage so that we're sure that the
	// uploader isn't trying to fetch them.
	resp, err := http.Get(currentTools.URL)
	c.Assert(err, IsNil)
	err = environs.UnpackTools(dataDir, currentTools, resp.Body)
	c.Assert(err, IsNil)
	s.removeVersion(c, currentTools.Binary)

	var (
		u            *Upgrader
		upgraderDone <-chan error
	)

	defer func() {
		if u != nil {
			c.Assert(u.Stop(), IsNil)
		}
	}()

	uploaded := make(map[version.Number]*state.Tools)
	for i, test := range upgraderTests {
		c.Logf("%d. %s; current version: %v", i, test.about, version.Current)
		for _, v := range test.upload {
			vers := version.Current
			vers.Number = version.MustParse(v)
			tools := s.uploadTools(c, vers)
			uploaded[vers.Number] = tools
		}
		if test.current != "" {
			version.Current = version.MustParseBinary(test.current)
			currentTools, err = environs.ReadTools(dataDir, version.Current)
			c.Assert(err, IsNil)
		}
		if u == nil {
			u = startUpgrader(c, s.State, dataDir, currentTools)
		}
		if test.propose != "" {
			s.proposeVersion(c, version.MustParse(test.propose), test.devVersion)
		}
		if test.upgradeTo == "" {
			assertNothingHappens(c, upgraderDone)
		} else {
			tools := uploaded[version.MustParse(test.upgradeTo)]
			waitDeath(c, u, tools, "")
			// Check that the upgraded version was really downloaded.
			data, err := ioutil.ReadFile(filepath.Join(environs.ToolsDir(dataDir, tools.Binary), "jujud"))
			c.Assert(err, IsNil)
			c.Assert(string(data), Equals, "jujud contents "+tools.Binary.String())

			u, upgraderDone = nil, nil
			currentTools = tools
			version.Current = tools.Binary
		}
	}
}

var delayedStopTests = []struct {
	about         string
	upgraderDelay time.Duration
	storageDelay  time.Duration
	propose       string
	err           string
}{{
	about:         "same version",
	upgraderDelay: time.Second,
	propose:       "2.0.3",
}, {
	about:         "same version found for higher proposed version",
	upgraderDelay: time.Second,
	propose:       "2.0.4",
}, {
	about:         "no appropriate version found",
	upgraderDelay: time.Second,
	propose:       "2.0.3",
}, {
	about:         "time out",
	propose:       "2.0.6",
	storageDelay:  time.Second,
	upgraderDelay: 10 * time.Millisecond,
	err:           "download aborted.*",
}, {
	about:         "successful upgrade",
	upgraderDelay: time.Second,
	propose:       "2.0.6",
	// enough delay that the stop will probably arrive before the
	// tools have downloaded, thus checking that the
	// upgrader really did wait for the download.
	storageDelay: 5 * time.Millisecond,
	err:          `must restart:.*2\.0\.6.*`,
}, {
	about:         "fetch error",
	upgraderDelay: time.Second,
	propose:       "2.0.7",
},
}

func (s *upgraderSuite) TestDelayedStop(c *C) {
	defer dummy.SetStorageDelay(0)
	dataDir, tools := s.primeTools(c, version.MustParseBinary("2.0.3-foo-bar"))
	s.uploadTools(c, version.MustParseBinary("2.0.5-foo-bar"))
	s.uploadTools(c, version.MustParseBinary("2.0.6-foo-bar"))
	s.uploadTools(c, version.MustParseBinary("2.0.6-foo-bar"))
	s.uploadTools(c, version.MustParseBinary("2.0.7-foo-bar"))
	s.poisonVersion(version.MustParseBinary("2.0.7-foo-bar"))

	for i, test := range delayedStopTests {
		c.Logf("%d. %v", i, test.about)
		upgraderDelay = test.upgraderDelay
		dummy.SetStorageDelay(test.storageDelay)
		proposed := version.MustParse(test.propose)
		s.proposeVersion(c, proposed, true)
		u := startUpgrader(c, s.State, dataDir, tools)
		t0 := time.Now()
		err := u.Stop()
		d := time.Now().Sub(t0)
		if d > 100*time.Millisecond {
			c.Errorf("upgrader took took too long: %v", d)
		}
		if test.err == "" {
			c.Check(err, IsNil)
		} else {
			c.Check(err, ErrorMatches, test.err)
		}
	}
}

func (s *upgraderSuite) poisonVersion(vers version.Binary) {
	path := environs.ToolsStoragePath(vers)
	dummy.Poison(s.Conn.Environ.Storage(), path, fmt.Errorf("poisoned file"))
}

func (s *upgraderSuite) removeVersion(c *C, vers version.Binary) {
	path := environs.ToolsStoragePath(vers)
	err := s.Conn.Environ.Storage().Remove(path)
	c.Assert(err, IsNil)
}

// primeTools sets up the current version of the tools to vers and
// makes sure that they're available in the returned dataDir.
func (s *upgraderSuite) primeTools(c *C, vers version.Binary) (dataDir string, tools *state.Tools) {
	dataDir = c.MkDir()
	// Set up the current version and tools.
	version.Current = vers
	tools = s.uploadTools(c, vers)
	resp, err := http.Get(tools.URL)
	c.Assert(err, IsNil)
	defer resp.Body.Close()
	err = environs.UnpackTools(dataDir, tools, resp.Body)
	c.Assert(err, IsNil)
	return dataDir, tools
}

func assertNothingHappens(c *C, upgraderDone <-chan error) {
	select {
	case got := <-upgraderDone:
		c.Fatalf("expected nothing to happen, got %v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func assertEvent(c *C, event <-chan string, want string) {
	select {
	case got := <-event:
		c.Assert(got, Equals, want)
	case <-time.After(500 * time.Millisecond):
		c.Fatalf("no event received; expected %q", want)
	}
}

// startUpgrader starts the upgrader using the given machine,
// expecting to see it set the given agent tools.
func startUpgrader(c *C, st *state.State, dataDir string, expectTools *state.Tools) *Upgrader {
	as := testAgentState(make(chan *state.Tools))
	u := NewUpgrader(st, as, dataDir)
	select {
	case tools := <-as:
		c.Assert(tools, DeepEquals, expectTools)
	case <-time.After(500 * time.Millisecond):
		c.Fatalf("upgrader did not set agent tools")
	}
	return u
}

func waitDeath(c *C, u *Upgrader, upgradeTo *state.Tools, errPat string) {
	done := make(chan error, 1)
	go func() {
		done <- u.Wait()
	}()
	select {
	case err := <-done:
		switch {
		case upgradeTo != nil:
			c.Assert(err, DeepEquals, &UpgradedError{upgradeTo})
		case errPat != "":
			c.Assert(err, ErrorMatches, errPat)
		default:
			c.Assert(err, IsNil)
		}
	case <-time.After(500 * time.Millisecond):
		c.Fatalf("upgrader did not die as expected")
	}
}

type testAgentState chan *state.Tools

func (as testAgentState) SetAgentTools(tools *state.Tools) error {
	t := *tools
	as <- &t
	return nil
}
