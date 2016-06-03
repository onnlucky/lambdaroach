package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"lambdaroach/shared"
	"log"
	"net"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

// command line flags
var host = flag.String("h", "", "[ssh:]host to connect with, default is ssh:app.hostname")
var port = flag.String("p", "8888", "port to connect, normal port is 8888")
var apppath = flag.String("d", ".", "application path, default is the current directory")
var appconfig = flag.String("f", "", "app config file, default is appdir/lambda.config.json or ./lambda.config.json")

// Config from lambda.config.json
type Config struct {
	Name     string   `json:"name"`
	Hostname string   `json:"hostname"`
	Command  string   `json:"command"`
	Env      []string `json:"env"`
}

func sendFiles(dir string, sub string, conn io.ReadWriter) (filecount int, bytecount int64) {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Fatal(err)
	}

	for _, file := range files {
		if file.Name()[0] == '.' {
			//log.Print("skipping hidden file: ", file.Name())
			continue
		}

		fullpath := path.Join(dir, file.Name())
		isdir := file.IsDir()
		isfile := file.Mode().IsRegular()
		if !(isdir || isfile) {
			// resolve links by trying and filling in isdir or isfile
			linkpath, err2 := os.Readlink(fullpath)
			if err2 == nil {
				if !shared.StartsWith(linkpath, "/") {
					linkpath = path.Join(dir, linkpath)
				}
				stat, err2 := os.Stat(linkpath)
				if err2 == nil {
					isdir = stat.Mode().IsDir()
					isfile = stat.Mode().IsRegular()
				}
			}
		}

		if isdir {
			ndir := path.Join(dir, file.Name())
			nsub := path.Join(sub, file.Name())
			file := shared.FileMessage{Name: nsub + "/"}
			err = shared.WriteJSON0(conn, file)
			if err != nil {
				log.Fatal(err)
			}
			// recurse
			fc, bc := sendFiles(ndir, nsub, conn)
			filecount += fc
			bytecount += bc
			continue
		}
		if !isfile {
			log.Print("skipping non file: ", file.Name())
			continue
		}

		// TODO stream file instead ...
		bytes, err := ioutil.ReadFile(path.Join(dir, file.Name()))
		if err != nil {
			log.Fatal(err)
		}
		file := shared.FileMessage{Name: path.Join(sub, file.Name()), Size: len(bytes)}
		err = shared.WriteJSON0(conn, file)
		if err != nil {
			log.Fatal(err)
		}

		written, err := conn.Write(bytes)
		if err != nil {
			log.Fatal(err)
		}
		if written != len(bytes) {
			log.Fatal("unable to write all bytes??")
		}
		filecount++
		bytecount += int64(written)
	}
	return
}

type combinedPipe struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
}

func (cp combinedPipe) Read(p []byte) (int, error) {
	return cp.Stdout.Read(p)
}

func (cp combinedPipe) Write(p []byte) (int, error) {
	return cp.Stdin.Write(p)
}

func (cp combinedPipe) Close() error {
	err1 := cp.Stdin.Close()
	err2 := cp.Stdout.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

func dialSSH(host string) (io.ReadWriteCloser, error) {
	path, err := exec.LookPath("ssh")
	if err != nil {
		log.Fatal(err)
	}
	var host2 string
	if shared.StartsWith(host, "ssh://") {
		host2 = host[len("ssh://"):]
	} else if shared.StartsWith(host, "ssh:") {
		host2 = host[len("ssh:"):]
	} else {
		host2 = host
	}

	// connect stdin/stdout with remote localhost port 8888
	cmd := exec.Command(path, fmt.Sprintf("-Wlocalhost:%s", *port), host2)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Fatal(err)
	}
	err = cmd.Start()
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		out := bufio.NewReader(stderr)
		for {
			line, err := out.ReadString('\n')
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Print("ssh client error: ", err)
				break
			}
			log.Print("> ", line)
		}
		err := cmd.Wait()
		if err != nil {
			log.Print("ssh client error: ", err)
		}
	}()
	time.Sleep(10 * time.Millisecond)
	return combinedPipe{stdin, stdout}, nil
}

func main() {
	log.SetFlags(log.Flags() | log.Lshortfile)
	log.SetPrefix(fmt.Sprintf("%s ", path.Base(os.Args[0])))
	flag.Parse()

	if apppath == nil || *apppath == "" || *appconfig == "./" {
		*apppath = "."
	}
	var appconfig1 = *appconfig
	var appconfig2 = ""
	if appconfig1 == "" && *apppath != "." {
		appconfig1 = path.Join(*apppath, "lambda.config.json")
		appconfig2 = "lambda.config.json"
	}

	version := flag.Arg(0)
	if version == "" {
		version = "none"
	}

	configfile := appconfig1
	bytes, err := ioutil.ReadFile(appconfig1)
	if err != nil {
		if appconfig2 != "" {
			configfile = appconfig2
			bytes, err = ioutil.ReadFile(appconfig2)
		}
	}
	if err != nil {
		if appconfig2 != "" {
			log.Fatal("unable to read app json file: ", appconfig1, " or ", appconfig2, " got: ", err)
		}
		log.Fatal("unable to read app json file: ", appconfig1, " got: ", err)
	}
	var config Config
	err = json.Unmarshal(bytes, &config)
	if err != nil {
		log.Fatal("unable to parse app json file: ", configfile, " got: ", err)
		return
	}

	if *host == "" {
		*host = "ssh:" + config.Hostname
	}

	log.Print("uploading app: ", config.Name, " version: ", version, " to: ", *host)

	var conn io.ReadWriteCloser
	//var err error
	if shared.StartsWith(*host, "ssh") {
		conn, err = dialSSH(*host)
		conn.Write([]byte{0, 0, 0, 0})
	} else {
		host2 := *host
		if !strings.Contains(host2, ":") {
			host2 = fmt.Sprintf("%s:%s", *host, *port)
		}
		conn, err = net.Dial("tcp", host2)
	}
	if err != nil {
		log.Fatal(err)
	}

	app := shared.AppMessage{
		Name:    config.Name,
		Version: version,
		Command: config.Command,
		Hosts:   []string{config.Hostname},
		Env:     config.Env,
	}

	err = shared.WriteJSON0(conn, app)
	if err != nil {
		log.Fatal(err)
	}

	in := bufio.NewReader(conn)
	var accept shared.Accept
	err = shared.ReadJSON0(in, &accept)
	if err != nil {
		log.Fatal(err)
	}
	log.Print("uploading app: ", app, " as: ", accept.ID)

	filecount, bytecount := sendFiles(*apppath, "", conn)
	file := shared.FileMessage{}
	err = shared.WriteJSON0(conn, file)
	if err != nil {
		log.Fatal(err)
	}

	log.Print("uploaded files: ", filecount, ", total bytes: ", bytecount)

	var status shared.Status
	err = shared.ReadJSON0(in, &status)
	if err != nil {
		log.Fatal(err)
	}
	if !status.Ok {
		log.Fatal(status.Msg)
	}
	log.Print("ok")
}
