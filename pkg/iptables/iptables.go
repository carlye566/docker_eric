package iptables

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"

	log "github.com/Sirupsen/logrus"
)

type Action string
type Table string

const (
	Append Action = "-A"
	Delete Action = "-D"
	Insert Action = "-I"
	Nat    Table  = "nat"
	Filter Table  = "filter"
	Mangle Table  = "mangle"
)

var (
	iptablesPath        string
	supportsXlock       = false
	ErrIptablesNotFound = errors.New("Iptables not found")
)

type Chain struct {
	Name   string
	Bridge string
	Table  Table
}

type ChainError struct {
	Chain  string
	Output []byte
}

func (e *ChainError) Error() string {
	return fmt.Sprintf("Error iptables %s: %s", e.Chain, string(e.Output))
}

func initCheck() error {

	if iptablesPath == "" {
		path, err := exec.LookPath("iptables")
		if err != nil {
			return ErrIptablesNotFound
		}
		iptablesPath = path
		supportsXlock = exec.Command(iptablesPath, "--wait", "-L", "-n").Run() == nil
	}
	return nil
}

func NewChain(name, bridge string, table Table) (*Chain, error) {
	c := &Chain{
		Name:   name,
		Bridge: bridge,
		Table:  table,
	}

	if string(c.Table) == "" {
		c.Table = Filter
	}

	// Add chain if it doesn't exist
	if _, err := Raw("-t", string(c.Table), "-n", "-L", c.Name); err != nil {
		if output, err := Raw("-t", string(c.Table), "-N", c.Name); err != nil {
			return nil, err
		} else if len(output) != 0 {
			return nil, fmt.Errorf("Could not create %s/%s chain: %s", c.Table, c.Name, output)
		}
	}

	switch table {
	case Nat:
		preroute := []string{
			"-m", "addrtype",
			"--dst-type", "LOCAL",
			"-j", c.Name}
		if !Exists(Nat, "PREROUTING", preroute...) {
			if err := c.Prerouting(Append, preroute...); err != nil {
				return nil, fmt.Errorf("Failed to inject docker in PREROUTING chain: %s", err)
			}
		}
		output := []string{
			"-m", "addrtype",
			"--dst-type", "LOCAL",
			"-j", c.Name,
			"!", "--dst", "127.0.0.0/8"}
		if !Exists(Nat, "OUTPUT", output...) {
			if err := c.Output(Append, output...); err != nil {
				return nil, fmt.Errorf("Failed to inject docker in OUTPUT chain: %s", err)
			}
		}
	case Filter:
		link := []string{
			"-o", c.Bridge,
			"-j", c.Name}
		if !Exists(Filter, "FORWARD", link...) {
			insert := append([]string{string(Insert), "FORWARD"}, link...)
			if output, err := Raw(insert...); err != nil {
				return nil, err
			} else if len(output) != 0 {
				return nil, fmt.Errorf("Could not create linking rule to %s/%s: %s", c.Table, c.Name, output)
			}
		}
	}
	return c, nil
}

func RemoveExistingChain(name string, table Table) error {
	c := &Chain{
		Name:  name,
		Table: table,
	}
	if string(c.Table) == "" {
		c.Table = Filter
	}
	return c.Remove()
}

func (c *Chain) ForwardRules(ip net.IP, port int, proto, destAddr string, destPort int) [][]string {
	var rules [][]string
	daddr := ip.String()
	if ip.IsUnspecified() {
		// iptables interprets "0.0.0.0" as "0.0.0.0/32", whereas we
		// want "0.0.0.0/0". "0/0" is correctly interpreted as "any
		// value" by both iptables and ip6tables.
		daddr = "0/0"
	}
	args := []string{c.Name, "-t", string(Nat),
		"-p", proto,
		"-d", daddr,
		"--dport", strconv.Itoa(port),
		"!", "-i", c.Bridge,
		"-j", "DNAT",
		"--to-destination", net.JoinHostPort(destAddr, strconv.Itoa(destPort))}
	rules = append(rules, args)
	rules = append(rules, []string{c.Name, "-t", string(Filter),
		"!", "-i", c.Bridge,
		"-o", c.Bridge,
		"-p", proto,
		"-d", destAddr,
		"--dport", strconv.Itoa(destPort),
		"-j", "ACCEPT"})
	rules = append(rules, []string{"POSTROUTING", "-t", string(Nat),
		"-p", proto,
		"-s", destAddr,
		"-d", destAddr,
		"--dport", strconv.Itoa(destPort),
		"-j", "MASQUERADE"})
	return rules
}

// Add forwarding rule to 'filter' table and corresponding nat rule to 'nat' table
func (c *Chain) Forward(action Action, ip net.IP, port int, proto, destAddr string, destPort int) error {
	return c.Multiple(action, c.ForwardRules(ip, port, proto, destAddr, destPort), "FORWARD")
}

// Cleanly add or delete multiple iptable rules
func (c *Chain) Multiple(action Action, rules [][]string, chain string) error {
	if action == Append || action == Insert {
		// Append or Insert rules
		for i, rule := range rules {
			if output, err := Raw(append([]string{string(action)}, rule...)...); err != nil || len(output) != 0 {
				fmt.Println("fail to create forward here")
				// Delete previous rules
				log.Debugf("cleaning previous succeed rules")
				for index, deleteRule := range rules {
					if index == i {
						break
					}
					Raw(append([]string{string(Delete)}, deleteRule...)...)
				}
				if err != nil {
					return err
				}
				return &ChainError{Chain: chain, Output: output}
			}
		}
		return nil
	} else if action == Delete {
		// Delete rules
		var retErr error
		for _, rule := range rules {
			if output, err := Raw(append([]string{string(action)}, rule...)...); err != nil || len(output) != 0 {
				// Do not return here, we have to try to delete all rules even if one of them fails
				if err != nil {
					retErr = err
				} else {
					retErr = &ChainError{Chain: chain, Output: output}
				}
			}
		}
		return retErr
	} else {
		return fmt.Errorf("Invalid IPTables action '%s'", string(action))
	}
}

func (c *Chain) LinkRules(ip1, ip2 net.IP, port int, proto string) [][]string {
	var rules [][]string
	rules = append(rules, []string{c.Name, "-t", string(Filter),
		"-i", c.Bridge, "-o", c.Bridge,
		"-p", proto,
		"-s", ip1.String(),
		"-d", ip2.String(),
		"--dport", strconv.Itoa(port),
		"-j", "ACCEPT"})
	rules = append(rules, []string{c.Name, "-t", string(Filter),
		"-i", c.Bridge, "-o", c.Bridge,
		"-p", proto,
		"-s", ip2.String(),
		"-d", ip1.String(),
		"--sport", strconv.Itoa(port),
		"-j", "ACCEPT"})
	return rules
}

// Add reciprocal ACCEPT rule for two supplied IP addresses.
// Traffic is allowed from ip1 to ip2 and vice-versa
func (c *Chain) Link(action Action, ip1, ip2 net.IP, port int, proto string) error {
	return c.Multiple(action, c.LinkRules(ip1, ip2, port, proto), "LINK")
}

// Add linking rule to nat/PREROUTING chain.
func (c *Chain) Prerouting(action Action, args ...string) error {
	a := []string{"-t", string(Nat), string(action), "PREROUTING"}
	if len(args) > 0 {
		a = append(a, args...)
	}
	if output, err := Raw(append(a)...); err != nil {
		return err
	} else if len(output) != 0 {
		return &ChainError{Chain: "PREROUTING", Output: output}
	}
	return nil
}

// Add linking rule to an OUTPUT chain
func (c *Chain) Output(action Action, args ...string) error {
	a := []string{"-t", string(c.Table), string(action), "OUTPUT"}
	if len(args) > 0 {
		a = append(a, args...)
	}
	if output, err := Raw(append(a)...); err != nil {
		return err
	} else if len(output) != 0 {
		return &ChainError{Chain: "OUTPUT", Output: output}
	}
	return nil
}

func (c *Chain) Remove() error {
	// Ignore errors - This could mean the chains were never set up
	if c.Table == Nat {
		c.Prerouting(Delete, "-m", "addrtype", "--dst-type", "LOCAL", "-j", c.Name)
		c.Output(Delete, "-m", "addrtype", "--dst-type", "LOCAL", "!", "--dst", "127.0.0.0/8", "-j", c.Name)
		c.Output(Delete, "-m", "addrtype", "--dst-type", "LOCAL", "-j", c.Name) // Created in versions <= 0.1.6

		c.Prerouting(Delete)
		c.Output(Delete)
	}
	Raw("-t", string(c.Table), "-F", c.Name)
	Raw("-t", string(c.Table), "-X", c.Name)
	return nil
}

// Check if a rule exists
func Exists(table Table, chain string, rule ...string) bool {
	if string(table) == "" {
		table = Filter
	}

	// iptables -C, --check option was added in v.1.4.11
	// http://ftp.netfilter.org/pub/iptables/changes-iptables-1.4.11.txt

	// try -C
	// if exit status is 0 then return true, the rule exists
	if _, err := Raw(append([]string{
		"-t", string(table), "-C", chain}, rule...)...); err == nil {
		return true
	}

	// parse "iptables -S" for the rule (this checks rules in a specific chain
	// in a specific table)
	rule_string := strings.Join(rule, " ")
	existingRules, _ := exec.Command("iptables", "-t", string(table), "-S", chain).Output()

	return strings.Contains(string(existingRules), rule_string)
}

// Call 'iptables' system command, passing supplied arguments
func Raw(args ...string) ([]byte, error) {

	if err := initCheck(); err != nil {
		return nil, err
	}
	if supportsXlock {
		args = append([]string{"--wait"}, args...)
	}

	log.Debugf("%s, %v", iptablesPath, args)

	output, err := exec.Command(iptablesPath, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("iptables failed: iptables %v: %s (%s)", strings.Join(args, " "), output, err)
	}

	// ignore iptables' message about xtables lock
	if strings.Contains(string(output), "waiting for it to exit") {
		output = []byte("")
	}

	return output, err
}

func Cleanup(cleanups [][]string) error {
	var ret error
	for _, str := range cleanups {
		_, err := Raw(str...)
		if err != nil {
			ret = err
			log.Errorf("Error clean iptables %s", str)
		}
	}
	return ret
}
