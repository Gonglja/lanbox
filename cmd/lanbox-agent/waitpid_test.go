//go:build windows

package main

import (
	"reflect"
	"testing"
)

func TestWaitPidArg(t *testing.T) {
	cases := []struct {
		args []string
		want uint32
	}{
		{[]string{"-wait-pid", "123"}, 123},
		{[]string{"--wait-pid", "456"}, 456},
		{[]string{"-wait-pid=789"}, 789},
		{[]string{"--wait-pid=42"}, 42},
		{[]string{"-wait-pid", "abc"}, 0},
		{[]string{"-wait-pid", "0"}, 0},
		{[]string{"-wait-pid"}, 0},
		{nil, 0},
		{[]string{"-autostart"}, 0},
	}
	for _, c := range cases {
		if got := waitPidArg(c.args); got != c.want {
			t.Fatalf("waitPidArg(%v) = %d，期望 %d", c.args, got, c.want)
		}
	}
}

func TestArgsWithoutWaitPid(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{[]string{"-wait-pid", "1", "-foo"}, []string{"-foo"}},
		{[]string{"--wait-pid=2", "-bar"}, []string{"-bar"}},
		{[]string{"-foo", "-wait-pid", "3"}, []string{"-foo"}},
		{nil, []string{}},
		{[]string{"-x"}, []string{"-x"}},
	}
	for _, c := range cases {
		if got := argsWithoutWaitPid(c.in); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("argsWithoutWaitPid(%v) = %v，期望 %v", c.in, got, c.want)
		}
	}
}
