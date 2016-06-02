package main

import (
	"testing"
)

func TestStartsWith(t *testing.T) {
	if !StartsWith("hello", "hello") {
		t.Fatal("oeps")
	}
	if StartsWith("hello", "foo") {
		t.Fatal("oeps")
	}
	if StartsWith("hello", "hello world") {
		t.Fatal("oeps")
	}
	if !StartsWith("hello world", "hello") {
		t.Fatal("oeps")
	}
	if !StartsWith("hello", "") {
		t.Fatal("oeps")
	}
}

func TestEndsWith(t *testing.T) {
	if !EndsWith("hello", "hello") {
		t.Fatal("oeps")
	}
	if EndsWith("hello", "foo") {
		t.Fatal("oeps")
	}
	if EndsWith("hello", "hello world") {
		t.Fatal("oeps")
	}
	if !EndsWith("hello world", "world") {
		t.Fatal("oeps")
	}
	if !EndsWith("hello", "") {
		t.Fatal("oeps")
	}
}
