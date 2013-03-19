package cmd_test

import (
	"io/ioutil"
	. "launchpad.net/gocheck"
	"launchpad.net/juju-core/cmd"
	"launchpad.net/juju-core/log"
	"launchpad.net/juju-core/testing"
	"path/filepath"
)

type LogSuite struct {
	testing.LoggingSuite
}

var _ = Suite(&LogSuite{})

func (s *LogSuite) TestAddFlags(c *C) {
	l := &cmd.Log{}
	f := testing.NewFlagSet()
	l.AddFlags(f)

	err := f.Parse(false, []string{})
	c.Assert(err, IsNil)
	c.Assert(l.Path, Equals, "")
	c.Assert(l.Verbose, Equals, false)
	c.Assert(l.Debug, Equals, false)

	err = f.Parse(false, []string{"--log-file", "foo", "--verbose", "--debug"})
	c.Assert(err, IsNil)
	c.Assert(l.Path, Equals, "foo")
	c.Assert(l.Verbose, Equals, true)
	c.Assert(l.Debug, Equals, true)
}

func (s *LogSuite) TestStart(c *C) {
	for _, t := range []struct {
		path    string
		verbose bool
		debug   bool
		check   []interface{}
	}{
		{"", true, true, []interface{}{NotNil}},
		{"", true, false, []interface{}{NotNil}},
		{"", false, true, []interface{}{NotNil}},
		{"", false, false, []interface{}{Equals, log.NilLogger}},
		{"foo", true, true, []interface{}{NotNil}},
		{"foo", true, false, []interface{}{NotNil}},
		{"foo", false, true, []interface{}{NotNil}},
		{"foo", false, false, []interface{}{NotNil}},
	} {
		// commands always start with the log target set to NilLogger
		log.SetTarget(log.NilLogger)

		l := &cmd.Log{Prefix: "test", Path: t.path, Verbose: t.verbose, Debug: t.debug}
		ctx := testing.Context(c)
		err := l.Start(ctx)
		c.Assert(err, IsNil)
		c.Assert(log.Target(), t.check[0].(Checker), t.check[1:]...)
		c.Assert(log.Debug, Equals, t.debug)
	}
}

func (s *LogSuite) TestStderr(c *C) {
	l := &cmd.Log{Prefix: "test", Verbose: true}
	ctx := testing.Context(c)
	err := l.Start(ctx)
	c.Assert(err, IsNil)
	log.Printf("hello")
	c.Assert(bufferString(ctx.Stderr), Matches, `JUJU:test:.* INFO: hello\n`)
}

func (s *LogSuite) TestRelPathLog(c *C) {
	l := &cmd.Log{Prefix: "test", Path: "foo.log"}
	ctx := testing.Context(c)
	err := l.Start(ctx)
	c.Assert(err, IsNil)
	log.Printf("hello")
	c.Assert(bufferString(ctx.Stderr), Equals, "")
	content, err := ioutil.ReadFile(filepath.Join(ctx.Dir, "foo.log"))
	c.Assert(err, IsNil)
	c.Assert(string(content), Matches, `JUJU:test:.* INFO: hello\n`)
}

func (s *LogSuite) TestAbsPathLog(c *C) {
	path := filepath.Join(c.MkDir(), "foo.log")
	l := &cmd.Log{Prefix: "test", Path: path}
	ctx := testing.Context(c)
	err := l.Start(ctx)
	c.Assert(err, IsNil)
	log.Printf("hello")
	c.Assert(bufferString(ctx.Stderr), Equals, "")
	content, err := ioutil.ReadFile(path)
	c.Assert(err, IsNil)
	c.Assert(string(content), Matches, `JUJU:test:.* INFO: hello\n`)
}
