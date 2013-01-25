package main

import (
	"fmt"
	"launchpad.net/gnuflag"
	"launchpad.net/juju-core/cmd"
	"launchpad.net/juju-core/juju"
	"launchpad.net/juju-core/state"
)

// DestroyServiceCommand causes an existing service to be destroyed.
type DestroyServiceCommand struct {
	EnvName     string
	ServiceName string
}

func (c *DestroyServiceCommand) Info() *cmd.Info {
	return &cmd.Info{
		"destroy-service", "<service>", "destroy a service",
		"Destroying a service will destroy all its units and relations.",
	}
}

func (c *DestroyServiceCommand) Init(f *gnuflag.FlagSet, args []string) error {
	addEnvironFlags(&c.EnvName, f)
	if err := f.Parse(true, args); err != nil {
		return err
	}
	args = f.Args()
	if len(args) == 0 {
		return fmt.Errorf("no service specified")
	}
	if !state.IsServiceName(args[0]) {
		return fmt.Errorf("invalid service name %q", args[0])
	}
	c.ServiceName, args = args[0], args[1:]
	return cmd.CheckEmpty(args)
}

func (c *DestroyServiceCommand) Run(_ *cmd.Context) error {
	conn, err := juju.NewConnFromName(c.EnvName)
	if err != nil {
		return err
	}
	defer conn.Close()
	svc, err := conn.State.Service(c.ServiceName)
	if err != nil {
		return err
	}
	return svc.Destroy()
}
