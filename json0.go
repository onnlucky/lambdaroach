package main

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
)

// AppMessage ...
type AppMessage struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Command string   `json:"command"`
	Hosts   []string `json:"hosts"`
	Env     []string `json:"env"`
}

// Accept ...
type Accept struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
}

// FileMessage ...
type FileMessage struct {
	Name string `json:"name"`
	Size int    `json:"size"`
	Perm int    `json:"perm"`
}

// Status ...
type Status struct {
	Ok  bool   `json:"status"`
	Msg string `json:"msg"`
}

// StartsWith check if string s starts with string prefix
func StartsWith(s, prefix string) bool {
	sn := len(s)
	pn := len(prefix)
	if sn < pn {
		return false
	}
	return s[0:pn] == prefix
}

// EndsWith check if string s ends with string postfix
func EndsWith(s, postfix string) bool {
	sn := len(s)
	pn := len(postfix)
	if sn < pn {
		return false
	}
	return s[sn-pn:sn] == postfix
}

// ReadJSON0 reads upto and including \0 from the reader and uses encoding/json.Unmarshal
func ReadJSON0(in *bufio.Reader, v interface{}) error {
	bytes, err := in.ReadBytes(byte(0))
	if err != nil {
		return err
	}
	err = json.Unmarshal(bytes[:len(bytes)-1], v)
	if err != nil {
		return err
	}
	return nil
}

// WriteJSON0 using encoding/json.Marshal writes the json and \0 to the Writer
func WriteJSON0(out io.Writer, v interface{}) error {
	bytes, err := json.Marshal(v)
	if err != nil {
		return err
	}
	written, err := out.Write(bytes)
	if err != nil {
		return err
	}
	if written != len(bytes) {
		log.Fatal("didn't write everything")
	}
	_, err = out.Write([]byte{0})
	if err != nil {
		return err
	}
	return nil
}
