package client

import (
	"fmt"

	"github.com/docker/docker/engine"
	"github.com/docker/docker/pkg/stringid"
	"text/tabwriter"
)

func (cli *DockerCli) CmdRegisterip(args ...string) error {
	cmd := cli.Subcmd("registerip", "[IP...]", "Register fixed ip", true)
	cmd.ParseFlags(args, true)

	if cmd.NArg() < 1 {
		cmd.Usage()
		return nil
	}

	var ipData engine.Env

	ipData.SetJson("ip", cmd.Args())
	_, _, err := cli.call("POST", "/ip/register", ipData, nil)
	if err != nil {
		return err
	}
	return nil
}

func (cli *DockerCli) CmdUnregisterip(args ...string) error {
	cmd := cli.Subcmd("unregisterip", "[IP...]", "UnRegister fixed ip", true)
	cmd.ParseFlags(args, true)

	if cmd.NArg() < 1 {
		cmd.Usage()
		return nil
	}

	var ipData engine.Env

	ipData.SetJson("ip", cmd.Args())
	_, _, err := cli.call("POST", "/ip/unregister", ipData, nil)
	if err != nil {
		return err
	}
	return nil
}

func (cli *DockerCli) CmdPrintip(args ...string) error {
	cmd := cli.Subcmd("printip", "", "Print fixed ip pool", true)
	if err := cmd.Parse(args); err != nil {
		return nil
	}
	body, _, err := readBody(cli.call("GET", "/ip/print", nil, nil))
	if err != nil {
		return err
	}
	outs := engine.NewTable("IP", 0)
	if _, err := outs.ReadListFrom(body); err != nil {
		return err
	}
	w := tabwriter.NewWriter(cli.out, 20, 1, 3, ' ', 0)
	fmt.Fprint(w, "IP\tCONTAINER\n")
	for _, out := range outs.Data {
		fmt.Fprintf(w, "%s\t%s\n", out.Get("IP"), stringid.TruncateID(out.Get("Container")))
	}
	w.Flush()
	return nil
}
