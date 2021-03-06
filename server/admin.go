package main

import (
	"bufio"
	"crypto/md5"
	"crypto/tls"
	"io"
	"io/ioutil"
	"lambdaroach/shared"
	"lambdaroach/uniuri"
	"log"
	"net"
	"os"
	"path"
	"time"
)

// only allow file mode permissions and setgit/setuid/sticky
func cleanFilePerm(perm int) os.FileMode {
	if perm == -1 {
		return 0 // all permissions off requires special value
	}
	if perm == 0 {
		return 0664 // missing or zero means default
	}
	return os.FileMode(perm) & (os.ModeSetgid | os.ModeSetuid | os.ModeSticky | os.ModePerm)
}

func writeFile(base string, file shared.FileMessage, r io.Reader) (int64, error) {
	out, err := os.OpenFile(path.Join(base, file.Name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, cleanFilePerm(file.Perm))
	if err != nil {
		return 0, err
	}
	defer out.Close()
	return io.Copy(out, r)
}

func cleanDirPerm(perm int) os.FileMode {
	if perm == -1 {
		return 0
	}
	if perm == 0 {
		return 0755
	}
	return os.FileMode(perm) & (os.ModeSetgid | os.ModeSetuid | os.ModeSticky | os.ModePerm)
}

func writeDir(base string, file shared.FileMessage) error {
	if !shared.EndsWith(file.Name, "/") {
		log.Fatal("bad writeDir")
	}
	if file.Size != 0 {
		log.Fatal("bad writeDir")
	}
	return os.Mkdir(path.Join(base, file.Name), cleanDirPerm(file.Perm))
}

func errorConnection(base string, conn net.Conn, msg string, cerr error) bool {
	log.Print("error receiving app: ", msg, " ", cerr)
	if base != "" {
		err := os.RemoveAll(base)
		if err != nil {
			log.Print(err)
		}
	}
	err := shared.WriteJSON0(conn, shared.Status{false, msg})
	if err != nil {
		log.Print(err)
	}
	return false
}

func handleConnection(conn net.Conn) bool {
	defer conn.Close()
	in := bufio.NewReader(conn)

	// skip first series of zeros, usefull for ssh and password/passphrase questions
	for {
		b, err := in.ReadByte()
		if err != nil {
			return errorConnection("", conn, "error reading connection", err)
		}
		if b != 0 {
			in.UnreadByte()
			break
		}
	}

	var app shared.AppMessage
	err := shared.ReadJSON0(in, &app)
	if err != nil {
		return errorConnection("", conn, "error reading first message", err)
	}
	log.Print("admin: preparing app ", app)

	id := uniuri.New()
	base := "/tmp/" + id
	err = os.MkdirAll(base, 0755)
	if err != nil {
		return errorConnection("", conn, "error creating app storage", err)
	}
	log.Print("accept app: ", app.Name, " as: ", id)

	var version = 1
	lastSite := findSite(app.Name)
	if lastSite != nil {
		version = lastSite.version + 1
	}

	err = shared.WriteJSON0(conn, shared.Accept{version, id})
	if err != nil {
		return errorConnection(base, conn, "error writing accept", err)
	}

	var needtls = 0
	if app.TLS {
		needtls = 2
	}
	var pem = []byte{}
	var key = []byte{}

	var files = 0
	var bytes = int64(0)
	for {
		var file shared.FileMessage
		err = shared.ReadJSON0(in, &file)
		if err != nil {
			return errorConnection(base, conn, "error reading file message", err)
		}
		if file.Name == "" && file.Size <= 0 {
			log.Print("received full file list: ", files, ", total bytes: ", bytes)
			break
		}

		if file.Size > 10*1024*1024 {
			return errorConnection(base, conn, "file size too large", nil)
		}

		if shared.EndsWith(file.Name, "/") && file.Size <= 0 {
			if base != "" {
				err := writeDir(base, file)
				if err != nil {
					return errorConnection(base, conn, "error creating dir", err)
				}
			}
			continue
		}

		files++
		bytes += int64(file.Size)
		filein := io.LimitReader(in, int64(file.Size))

		if needtls == 2 {
			needtls = 1
			pem, err = ioutil.ReadAll(filein)
			if err != nil {
				return errorConnection(base, conn, "error reading pem", err)
			}
			log.Print("got private certificate: ", len(pem))
			continue
		} else if needtls == 1 {
			needtls = 0
			key, err = ioutil.ReadAll(filein)
			if err != nil {
				return errorConnection(base, conn, "error reading key", err)
			}
			log.Print("got private key: ", len(key))
			continue
		}

		_, err2 := writeFile(base, file, filein)
		if err2 != nil {
			return errorConnection(base, conn, "error creating file", err)
		}
		//log.Print("file: ", file.Name, " size: ", file.Size)
	}

	err = shared.WriteJSON0(conn, shared.Status{true, ""})
	if err != nil {
		log.Print(err)
	}

	var certid = []byte{}
	if len(pem) > 0 && len(key) > 0 {
		h := md5.New()
		h.Write(pem)
		h.Write(key)
		certid = h.Sum(nil)
		if !hasCertificate(certid) {
			log.Print("adding certificate to https")
			cert, err2 := tls.X509KeyPair(pem, key)
			if err2 != nil {
				log.Print(err2)
			} else {
				addCertificate(cert, certid)
			}
		}
	}

	if app.LetsEncryptEmail != "" {
		if letsEncrypt.Registered() {
			log.Print("letsencrypt already registered")
		} else {
			// TODO this is done only once :( ... should be more flexible
			log.Print("registering at letsencrypt.org: ", app.LetsEncryptEmail, app.Hosts)
			letsEncrypt.SetHosts(app.Hosts)
			letsEncrypt.Register(app.LetsEncryptEmail, nil)
		}
	}

	log.Print("adding site to server: ", app.Name, " ", version)
	addSite(&Site{
		id:        app.Name,
		version:   version,
		hostnames: app.Hosts,
		paths:     []string{"/"},
		env:       app.Env,
		command:   app.Command,
		data:      base,
		certid:    certid,
		httpsOnly: app.HTTPSOnly,
	})
	return true
}

func serveAdmin() {
	ln, err := net.Listen("tcp", "localhost:8888")
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("admin listening on port: %s", ln.Addr())
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Print("Error in admin accept: ", err)
			time.Sleep(50 * time.Millisecond)
		}
		go handleConnection(conn)
	}
}
