package main

import (
	"io/ioutil"
	"launchpad.net/gnuflag"
	. "launchpad.net/gocheck"
	"launchpad.net/juju-core/juju/cmd"
	"launchpad.net/juju-core/juju/environs"
	"launchpad.net/juju-core/juju/environs/dummy"
	"launchpad.net/juju-core/juju/testing"
	"os"
	"path/filepath"
	"reflect"
)

type cmdSuite struct {
	testing.LoggingSuite
	home string
}

var _ = Suite(&cmdSuite{})

// N.B. Barking is broken.
var config = `
default:
    peckham
environments:
    peckham:
        type: dummy
        zookeeper: false
    walthamstow:
        type: dummy
        zookeeper: false
    barking:
        type: dummy
        broken: true
        zookeeper: false
`

func (s *cmdSuite) SetUpTest(c *C) {
	s.LoggingSuite.SetUpTest(c)
	// Arrange so that the "home" directory points
	// to a temporary directory containing the config file.
	s.home = os.Getenv("HOME")
	dir := c.MkDir()
	os.Setenv("HOME", dir)
	err := os.Mkdir(filepath.Join(dir, ".juju"), 0777)
	c.Assert(err, IsNil)
	err = ioutil.WriteFile(filepath.Join(dir, ".juju", "environments.yaml"), []byte(config), 0666)
	c.Assert(err, IsNil)
}

func (s *cmdSuite) TearDownTest(c *C) {
	os.Setenv("HOME", s.home)

	dummy.Reset()
	s.LoggingSuite.TearDownTest(c)
}

func newFlagSet() *gnuflag.FlagSet {
	return gnuflag.NewFlagSet("", gnuflag.ContinueOnError)
}

// testInit checks that a command initialises correctly
// with the given set of arguments.
func testInit(c *C, com cmd.Command, args []string, errPat string) {
	err := com.Init(newFlagSet(), args)
	if errPat != "" {
		c.Assert(err, ErrorMatches, errPat)
	} else {
		c.Assert(err, IsNil)
	}
}

// assertConnName asserts that the Command is using
// the given environment name.
// Since every command has a different type,
// we use reflection to look at the value of the
// Conn field in the value.
func assertConnName(c *C, com cmd.Command, name string) {
	v := reflect.ValueOf(com).Elem().FieldByName("EnvName")
	c.Assert(v.IsValid(), Equals, true)
	c.Assert(v.Interface(), Equals, name)
}

// All members of EnvironmentInitTests are tested for the -environment and -e
// flags, and that extra arguments will cause parsing to fail.
var EnvironmentInitTests = []func() (cmd.Command, []string){
	func() (cmd.Command, []string) { return new(BootstrapCommand), nil },
	func() (cmd.Command, []string) { return new(DestroyCommand), nil },
	func() (cmd.Command, []string) {
		return new(DeployCommand), []string{"charm-name", "service-name"}
	},
}

// TestEnvironmentInit tests that all commands which accept
// the --environment variable initialise their
// environment name correctly.
func (*cmdSuite) TestEnvironmentInit(c *C) {
	for i, cmdFunc := range EnvironmentInitTests {
		c.Logf("test %d", i)
		com, args := cmdFunc()
		testInit(c, com, args, "")
		assertConnName(c, com, "")

		com, args = cmdFunc()
		testInit(c, com, append(args, "-e", "walthamstow"), "")
		assertConnName(c, com, "walthamstow")

		com, args = cmdFunc()
		testInit(c, com, append(args, "--environment", "walthamstow"), "")
		assertConnName(c, com, "walthamstow")

		com, args = cmdFunc()
		testInit(c, com, append(args, "hotdog"), "unrecognized args.*")
	}
}

func runCommand(com cmd.Command, args ...string) (opc chan dummy.Operation, errc chan error) {
	errc = make(chan error, 1)
	opc = make(chan dummy.Operation)
	dummy.Reset()
	dummy.Listen(opc)
	go func() {
		// signal that we're done with this ops channel.
		defer dummy.Listen(nil)

		err := com.Init(newFlagSet(), args)
		if err != nil {
			errc <- err
			return
		}

		err = com.Run(cmd.DefaultContext())
		errc <- err
	}()
	return
}

func op(kind dummy.OperationKind, name string) dummy.Operation {
	return dummy.Operation{
		Env:  name,
		Kind: kind,
	}
}

func (*cmdSuite) TestBootstrapCommand(c *C) {
	// normal bootstrap
	opc, errc := runCommand(new(BootstrapCommand))
	c.Check(<-opc, Equals, op(dummy.OpBootstrap, "peckham"))
	c.Check(<-errc, IsNil)

	// bootstrap with tool uploading - checking that a file
	// is uploaded should be sufficient, as the detailed semantics
	// of UploadTools are tested in environs.
	opc, errc = runCommand(new(BootstrapCommand), "--upload-tools")
	c.Check(<-opc, Equals, op(dummy.OpPutFile, "peckham"))
	c.Check(<-opc, Equals, op(dummy.OpBootstrap, "peckham"))
	c.Check(<-errc, IsNil)

	envs, err := environs.ReadEnvirons("")
	c.Assert(err, IsNil)
	env, err := envs.Open("peckham")
	c.Assert(err, IsNil)
	dir := c.MkDir()
	err = environs.GetTools(env, dir)
	c.Assert(err, IsNil)

	// bootstrap with broken environment
	opc, errc = runCommand(new(BootstrapCommand), "-e", "barking")
	c.Check((<-opc).Kind, Equals, dummy.OpNone)
	c.Check(<-errc, ErrorMatches, `broken environment`)
}

func (*cmdSuite) TestDestroyCommand(c *C) {
	// normal destroy
	opc, errc := runCommand(new(DestroyCommand))
	c.Check(<-opc, Equals, op(dummy.OpDestroy, "peckham"))
	c.Check(<-errc, IsNil)

	// destroy with broken environment
	opc, errc = runCommand(new(DestroyCommand), "-e", "barking")
	c.Check((<-opc).Kind, Equals, dummy.OpNone)
	c.Check(<-errc, ErrorMatches, `broken environment`)
}

var deployTests = []struct {
	args []string
	com  *DeployCommand
}{
	{
		[]string{"charm-name"},
		&DeployCommand{},
	}, {
		[]string{"charm-name", "service-name"},
		&DeployCommand{ServiceName: "service-name"},
	}, {
		[]string{"--config", "/path/to/config.yaml", "charm-name"},
		&DeployCommand{ConfPath: "/path/to/config.yaml"},
	}, {
		[]string{"--repository", "/path/to/another-repo", "charm-name"},
		&DeployCommand{RepoPath: "/path/to/another-repo"},
	}, {
		[]string{"--upgrade", "charm-name"},
		&DeployCommand{Upgrade: true},
	}, {
		[]string{"-u", "charm-name"},
		&DeployCommand{Upgrade: true},
	}, {
		[]string{"--num-units", "33", "charm-name"},
		&DeployCommand{NumUnits: 33},
	}, {
		[]string{"-n", "104", "charm-name"},
		&DeployCommand{NumUnits: 104},
	},
}

func initExpectations(com *DeployCommand) {
	if com.CharmName == "" {
		com.CharmName = "charm-name"
	}
	if com.NumUnits == 0 {
		com.NumUnits = 1
	}
	if com.RepoPath == "" {
		com.RepoPath = "/path/to/repo"
	}
}

func initDeployCommand(args ...string) (*DeployCommand, error) {
	com := &DeployCommand{}
	return com, com.Init(newFlagSet(), args)
}

func (*cmdSuite) TestDeployCommandInit(c *C) {
	defer os.Setenv("JUJU_REPOSITORY", os.Getenv("JUJU_REPOSITORY"))
	os.Setenv("JUJU_REPOSITORY", "/path/to/repo")

	for _, t := range deployTests {
		initExpectations(t.com)
		com, err := initDeployCommand(t.args...)
		c.Assert(err, IsNil)
		c.Assert(com, DeepEquals, t.com)
	}

	// missing args
	_, err := initDeployCommand()
	c.Assert(err, ErrorMatches, "no charm specified")

	// bad unit count
	_, err = initDeployCommand("charm-name", "--num-units", "0")
	c.Assert(err, ErrorMatches, "must deploy at least one unit")

	// environment tested elsewhere
}