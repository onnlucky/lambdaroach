package shared

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
)

// AppMessage ...
type AppMessage struct {
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	Command          string   `json:"command"`
	Hosts            []string `json:"hosts"`
	Env              []string `json:"env"`
	TLS              bool     `json:"tls"`
	LetsEncryptEmail string   `json:"letsencryptmail"`
	HTTPSOnly        bool     `json:"httpsonly"`
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

// Copy is same as io.Copy but, does not do dst.WriteFrom(src), but returns both writer errors and reader errors
func Copy(dst io.Writer, src io.Reader) (written int64, werr, rerr error) {
	buf := make([]byte, 32*1024)
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				werr = ew
				break
			}
			if nr != nw {
				werr = io.ErrShortWrite
				break
			}
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			rerr = er
			break
		}
	}
	return written, werr, rerr
}
