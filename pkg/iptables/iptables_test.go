package iptables

import (
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

const chainName = "DOCKERTEST"

var natChain *Chain
var filterChain *Chain

func TestNewChain(t *testing.T) {
	var err error

	natChain, err = NewChain(chainName, "lo", Nat)
	if err != nil {
		t.Fatal(err)
	}

	filterChain, err = NewChain(chainName, "lo", Filter)
	if err != nil {
		t.Fatal(err)
	}
}

func TestForward(t *testing.T) {
	ip := net.ParseIP("192.168.1.1")
	port := 1234
	dstAddr := "172.17.0.1"
	dstPort := 4321
	proto := "tcp"

	err := natChain.Forward(Insert, ip, port, proto, dstAddr, dstPort)
	if err != nil {
		t.Fatal(err)
	}

	dnatRule := []string{
		"!", "-i", filterChain.Bridge,
		"-d", ip.String(),
		"-p", proto,
		"--dport", strconv.Itoa(port),
		"-j", "DNAT",
		"--to-destination", dstAddr + ":" + strconv.Itoa(dstPort),
	}

	if !Exists(natChain.Table, natChain.Name, dnatRule...) {
		t.Fatalf("DNAT rule does not exist")
	}

	filterRule := []string{
		"!", "-i", filterChain.Bridge,
		"-o", filterChain.Bridge,
		"-d", dstAddr,
		"-p", proto,
		"--dport", strconv.Itoa(dstPort),
		"-j", "ACCEPT",
	}

	if !Exists(filterChain.Table, filterChain.Name, filterRule...) {
		t.Fatalf("filter rule does not exist")
	}

	masqRule := []string{
		"-d", dstAddr,
		"-s", dstAddr,
		"-p", proto,
		"--dport", strconv.Itoa(dstPort),
		"-j", "MASQUERADE",
	}

	if !Exists(natChain.Table, "POSTROUTING", masqRule...) {
		t.Fatalf("MASQUERADE rule does not exist")
	}

	// Clean up the rules in case of disturbing other tests
	natChain.Forward(Delete, ip, port, proto, dstAddr, dstPort)
	testCleanupForward(t, natChain.ForwardRules(ip, port, proto, dstAddr, dstPort), "TestForward")
}

func TestLink(t *testing.T) {
	var err error

	ip1 := net.ParseIP("192.168.1.1")
	ip2 := net.ParseIP("192.168.1.2")
	port := 1234
	proto := "tcp"

	err = filterChain.Link(Append, ip1, ip2, port, proto)
	if err != nil {
		t.Fatal(err)
	}

	rule1 := []string{
		"-i", filterChain.Bridge,
		"-o", filterChain.Bridge,
		"-p", proto,
		"-s", ip1.String(),
		"-d", ip2.String(),
		"--dport", strconv.Itoa(port),
		"-j", "ACCEPT"}

	if !Exists(filterChain.Table, filterChain.Name, rule1...) {
		t.Fatalf("rule1 does not exist")
	}

	rule2 := []string{
		"-i", filterChain.Bridge,
		"-o", filterChain.Bridge,
		"-p", proto,
		"-s", ip2.String(),
		"-d", ip1.String(),
		"--sport", strconv.Itoa(port),
		"-j", "ACCEPT"}

	if !Exists(filterChain.Table, filterChain.Name, rule2...) {
		t.Fatalf("rule2 does not exist")
	}
}

func TestPrerouting(t *testing.T) {
	args := []string{
		"-i", "lo",
		"-d", "192.168.1.1"}

	err := natChain.Prerouting(Insert, args...)
	if err != nil {
		t.Fatal(err)
	}

	rule := []string{
		"-j", natChain.Name}

	rule = append(rule, args...)

	if !Exists(natChain.Table, "PREROUTING", rule...) {
		t.Fatalf("rule does not exist")
	}

	delRule := append([]string{"-D", "PREROUTING", "-t", string(Nat)}, rule...)
	if _, err = Raw(delRule...); err != nil {
		t.Fatal(err)
	}
}

func TestOutput(t *testing.T) {
	args := []string{
		"-o", "lo",
		"-d", "192.168.1.1"}

	err := natChain.Output(Insert, args...)
	if err != nil {
		t.Fatal(err)
	}

	rule := []string{
		"-j", natChain.Name}

	rule = append(rule, args...)

	if !Exists(natChain.Table, "OUTPUT", rule...) {
		t.Fatalf("rule does not exist")
	}

	delRule := append([]string{"-D", "OUTPUT", "-t",
		string(natChain.Table)}, rule...)
	if _, err = Raw(delRule...); err != nil {
		t.Fatal(err)
	}
}

func TestMultiple(t *testing.T) {
	var err error
	ip := net.ParseIP("192.168.1.1")
	port := 1234
	dstAddr := "172.17.0.1"
	dstPort := 4321
	proto := "tcp"
	forwardRules := natChain.ForwardRules(ip, port, proto, dstAddr, dstPort)
	// test when creating multiple rules, if one of them fails, should cleanup the previously succeeded ones
	// remove filter chain, so the DNAT rule should been created first and removed because of failed to create filter rule
	removeFilterChain(t)
	if err = natChain.Multiple(Append, forwardRules, "FORWARD"); err == nil {
		//since filter chain is removed, create forward rules should fail
		t.Fatalf("should fail to create forward rules")
	}
	// no rules should exist
	testCleanupForward(t, forwardRules, "TestMultiple")

	if filterChain, err = NewChain(chainName, "lo", Filter); err != nil {
		t.Fatal(err)
	}

	//test if failed to cleanup the first one rule, should continue to cleanup all rules
	masquerade := []string{
		"-p", proto,
		"-s", dstAddr,
		"-d", strconv.Itoa(dstPort),
		"--dport", strconv.Itoa(port),
		"-j", "MASQUERADE"}
	//
	if _, err = Raw(append([]string{string(Append), "POSTROUTING", "-t", string(Nat)}, masquerade...)...); err != nil {
		t.Fatal("Failed to create masquerade rules")
	}

	if err = natChain.Multiple(Delete, forwardRules, "FORWARD"); err == nil {
		// since we didn't create DNAT rule, should fail
		t.Fatal("should fail to delete forward rules")
	}
	// no rules should exist
	testCleanupForward(t, forwardRules, "TestMultiple")
}

func TestCleanup(t *testing.T) {
	var err error
	var rules []byte

	// Cleanup filter/FORWARD first otherwise output of iptables-save is dirty
	link := []string{"-t", string(filterChain.Table),
		string(Delete), "FORWARD",
		"-o", filterChain.Bridge,
		"-j", filterChain.Name}
	if _, err = Raw(link...); err != nil {
		t.Fatal(err)
	}
	filterChain.Remove()

	err = RemoveExistingChain(chainName, Nat)
	if err != nil {
		t.Fatal(err)
	}

	rules, err = exec.Command("iptables-save").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rules), chainName) {
		t.Fatalf("Removing chain failed. %s found in iptables-save", chainName)
	}
}

func testCleanupForward(t *testing.T, rules [][]string, testCase string) {
	if Exists(natChain.Table, natChain.Name, rules[0]...) {
		t.Fatalf("DNAT rule exists in test case %s", testCase)
	}

	if Exists(filterChain.Table, filterChain.Name, rules[1]...) {
		t.Fatalf("FILTER rule exists in test case %s", testCase)
	}

	if Exists(natChain.Table, "POSTROUTING", rules[2]...) {
		t.Fatalf("MASQUERADE rule exists in test case %s", testCase)
	}
}

func removeFilterChain(t *testing.T) {
	link := []string{"-t", string(filterChain.Table),
		string(Delete), "FORWARD",
		"-o", filterChain.Bridge,
		"-j", filterChain.Name}
	if _, err := Raw(link...); err != nil {
		t.Fatal(err)
	}
	filterChain.Remove()
}
