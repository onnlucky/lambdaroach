package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"lambdaroach/shared"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"rsc.io/letsencrypt"
)

// RunningSite is an up and running application server
type RunningSite struct {
	id      int32
	addr    string
	pidfile string
	cmd     *exec.Cmd
	start   time.Time
	error   bool
	working int64
}

// PidFile returns the pidfile
func (run *RunningSite) PidFile() string {
	return fmt.Sprintf("%d.pid", run.id)
}

// Site is the static description of an application server
type Site struct {
	id        string
	version   int
	hostnames []string
	paths     []string
	env       []string // {"NODE_PRODUCTION=true", ... }
	command   string
	data      string // path where the data resides
	running   *RunningSite
	certid    []byte
	static    *http.Handler
	httpsOnly bool // redirect to https
}

var lock = sync.RWMutex{}
var launchlock = sync.Mutex{}
var sites []*Site
var latestSites []*Site
var routes = make(map[string][]*Site)
var port = 15000
var letsEncrypt = letsencrypt.Manager{}

type byVersion []*Site

func (a byVersion) Len() int           { return len(a) }
func (a byVersion) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byVersion) Less(i, j int) bool { return a[i].version > a[j].version }

func addSite(site *Site) {
	log.Print("adding site:", site.hostnames)

	lock.Lock()
	defer lock.Unlock()

	added := false
	for i, s := range latestSites {
		if s.id == site.id {
			if s.version == site.version {
				panic("registering known site")
			}
			latestSites[i] = site
			added = true
			break
		}
	}
	if !added {
		latestSites = append(latestSites, site)
	}

	sites = append(sites, site)
	for _, host := range site.hostnames {
		routes[host] = append(routes[host], site)
		sort.Sort(byVersion(routes[host]))
	}

	if len(latestSites) == 1 {
		routes["localhost"] = append(routes["localhost"], site)
		sort.Sort(byVersion(routes["localhost"]))
	} else {
		routes["localhost"] = []*Site{}
	}
}

func findSite(id string) *Site {
	lock.Lock()
	defer lock.Unlock()
	var res *Site
	for _, s := range sites {
		if s.id == id {
			if res == nil || res.version < s.version {
				res = s
			}
		}
	}
	return res
}

func matchSite(host, path string) (*Site, *RunningSite) {
	lock.RLock()
	defer lock.RUnlock()
	sites := routes[host]
	for _, site := range sites {
		for _, prefix := range site.paths {
			if shared.StartsWith(path, prefix) {
				return site, site.running
			}
		}
	}
	return nil, nil
}

func readlog(r io.Reader) {
	in := bufio.NewReader(r)
	for {
		line, err := in.ReadString('\n')
		if err == io.EOF {
			return
		}
		if err != nil {
			log.Print(err)
			return
		}
		log.Print(line)
	}
}

func launch(site Site) (*RunningSite, error) {
	log.Print("launching app: ", site.id, " ", site.version, " ", site.hostnames)
	id := rand.Int31()
	port++
	ports := fmt.Sprintf("%d", port)
	run := &RunningSite{id: id, addr: fmt.Sprintf("localhost:%s", ports), pidfile: fmt.Sprintf("%d.pid", id), start: time.Now()}

	// figure out path of executable
	split := strings.Split(strings.Replace(site.command, "${PORT}", ports, -1), " ")
	path, err := exec.LookPath(split[0])
	if err != nil {
		return run, err
	}

	// build command
	run.cmd = exec.Command(path, split[1:]...)
	run.cmd.Dir = site.data
	env := os.Environ()
	env = append(env, site.env...)
	env = append(env, fmt.Sprintf("PORT=%s", ports))
	run.cmd.Env = env

	// hook up stderr/stdout to logger
	stdout, err := run.cmd.StdoutPipe()
	if err != nil {
		return run, err
	}
	stderr, err := run.cmd.StderrPipe()
	if err != nil {
		return run, err
	}

	// start and write pidfile
	if err := run.cmd.Start(); err != nil {
		return run, err
	}
	if err := ioutil.WriteFile(run.pidfile, []byte(fmt.Sprintf("%d", run.cmd.Process.Pid)), 0644); err != nil {
		run.cmd.Process.Kill()
		return run, err
	}

	// run loggers
	go readlog(stdout)
	go readlog(stderr)
	log.Print("launched app: ", site.id, " ", run.id, " pid: ", run.cmd.Process.Pid, " port: ", ports)

	// set time again incase launching takes a while
	run.start = time.Now()
	return run, nil
}

func stop(site *Site, running *RunningSite, err error) {
	if err != nil {
		log.Print("stopping site due to error: ", err)
	}

	// bleed out by clearing the site.running field (under lock)
	func() {
		lock.Lock()
		defer lock.Unlock()
		if site.running == running {
			site.running = nil
			return
		}
		running = nil
	}()

	// only the process that clears the running field needs to close it up
	if running == nil {
		return
	}
	// this would be weird
	if site.running == running {
		log.Fatal("still site.running == running")
	}

	// wait until running.working drops to zero, then stop the app, or forces stop after X time
	go func() {
		tries := 0
		for {
			tries++
			stillrunning := atomic.LoadInt64(&running.working)
			if stillrunning > 0 {
				continue
			}
			if stillrunning < 0 {
				log.Fatal("running.working < 1")
			}
			if stillrunning == 0 {
				break
			}
			if tries > 100 {
				log.Print("force stopping app: ", site.id, " ", running.id)
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		running.cmd.Process.Kill()
		status, err := running.cmd.Process.Wait()
		if err != nil {
			log.Fatal(err)
		}
		log.Print("stopped app: ", site.id, " ", running.id, " pid: ", running.cmd.Process.Pid, " status: ", status)
	}()
}

// blindly write status
func write404(w http.ResponseWriter, r *http.Request, start time.Time) {
	w.WriteHeader(404)
	w.Write([]byte("404 Not Found"))
	log.Printf("%s %s 404 %0.3f", r.Method, r.RequestURI, time.Since(start).Seconds())
}

func write500(w http.ResponseWriter, r *http.Request, start time.Time, msg string) {
	w.WriteHeader(500)
	w.Write([]byte("500 Internal Error"))
	log.Printf("%s %s 500 %0.3f (%s)", r.Method, r.RequestURI, time.Since(start).Seconds(), msg)
}

func serveStatic(site *Site, w http.ResponseWriter, r *http.Request) {
	if site.static == nil {
		func() {
			lock.Lock()
			defer lock.Unlock()
			static := http.FileServer(http.Dir(site.data))
			site.static = &static
		}()
	}
	static := *site.static
	static.ServeHTTP(w, r)
}

// this receives the http requests, checks what to do, and replies
func serve(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	host := strings.Split(r.Host, ":")[0]
	path := r.RequestURI
	site, running := matchSite(host, path)

	if site == nil {
		write404(w, r, start)
		return
	}

	if site.httpsOnly && r.TLS == nil {
		if r.Host == "" {
			write404(w, r, start)
			return
		}

		host, _, _ := net.SplitHostPort(r.Host)
		// TODO perhaps we want to know the https port, incase it is 4443
		u := r.URL
		u.Host = host
		u.Scheme = "https"
		http.Redirect(w, r, u.String(), 302)
		log.Print("redirected to: ", u.String())
		return
	}

	if site.command == "" {
		serveStatic(site, w, r)
		return
	}

	if running != nil && running.error {
		if time.Since(running.start).Seconds() >= 5 {
			log.Print("removing error app: ", site.id, " ", running.id)
			func() {
				lock.Lock()
				defer lock.Unlock()
				site.running = nil
				running = nil
			}()
		}
	}

	if running == nil {
		func() {
			// take launchlock and then decide to launch
			launchlock.Lock()
			defer launchlock.Unlock()
			if site.running != nil {
				running = site.running // not site.running set while holding both locks
				return
			}

			var err error
			running, err = launch(*site)
			if err != nil {
				log.Print("launch error: ", site.id, " ", running.id, " err: ", err)
				running.error = true
			}

			// only here also take lock, so launching does not hold back old requests
			lock.Lock()
			defer lock.Unlock()
			site.running = running
		}()
	}

	if running.error {
		write500(w, r, start, "app in error")
		return
	}

	atomic.AddInt64(&running.working, 1)
	defer atomic.AddInt64(&running.working, -1)

	// TODO if we could somehow associate data with this connection, we can match a client tcp/ip connection with downstream tcp/ip connection
	// TODO websockets support by recognizing upgrade and hijacking the connection
	// TODO https support per site, and allow CONNECT

	// connect to app and send request downstream
	var conn net.Conn
	var err error
	if time.Since(running.start).Seconds() < 20 {
		// if just started, allow some grace
		for {
			conn, err = net.Dial("tcp", running.addr)
			if err == nil {
				break
			}
			if time.Since(running.start).Seconds() >= 20 {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	} else {
		conn, err = net.Dial("tcp", running.addr)
		// TODO if err, relaunch and retry this part
	}
	if err != nil {
		write500(w, r, start, "connecting to app")
		stop(site, running, err)
		return
	}

	// append to, or set the X-Forwarded-For header
	clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		if prior, ok := r.Header["X-Forwarded-For"]; ok {
			clientIP = strings.Join(prior, ", ") + ", " + clientIP
		}
		r.Header.Set("X-Forwarded-For", clientIP)
	}

	// extra security if tls
	if r.TLS != nil {
		w.Header().Add("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
	}

	// and write the request that came in to the downstream connection
	err = r.Write(conn)
	if err != nil {
		write500(w, r, start, "writing to app")
		stop(site, running, err)
		return
	}

	// read reply and send it back
	res, err := http.ReadResponse(bufio.NewReader(conn), r)
	if err != nil {
		write500(w, r, start, "reading from app")
		stop(site, running, err)
		return
	}

	// if it was a 500 error, assume the site is borked and stop it
	// the current will bleed out, a new one will be immediately started on a next request
	// we will however pass on the reply
	if res.StatusCode >= 500 {
		stop(site, running, nil)
	}

	header := w.Header()
	for k := range header {
		header[k] = nil
	}
	for k, v := range res.Header {
		header[k] = v
	}
	w.WriteHeader(res.StatusCode)

	defer res.Body.Close()
	_, werr, rerr := shared.Copy(w, res.Body)
	if werr != nil {
		log.Print("client write error: ", err)
	}
	if rerr != nil {
		stop(site, running, nil)
		return
	}
	log.Printf("%s %s %d %0.3f", r.Method, r.RequestURI, res.StatusCode, time.Since(start).Seconds())
}

var tlsLock = sync.RWMutex{}
var tlsConfig = &tls.Config{}
var certHashes = [][]byte{}

func hasCertificate(hash []byte) bool {
	tlsLock.RLock()
	defer tlsLock.RUnlock()

	for _, v := range certHashes {
		if bytes.Equal(hash, v) {
			return true
		}
	}
	return false
}

func addCertificate(cert tls.Certificate, hash []byte) {
	tlsLock.Lock()
	defer tlsLock.Unlock()

	for _, v := range certHashes {
		if bytes.Equal(hash, v) {
			return
		}
	}

	tlsConfig.Certificates = append(tlsConfig.Certificates, cert)
	tlsConfig.BuildNameToCertificate()
	certHashes = append(certHashes, hash)
}

func removeCertificate(hash []byte) {
	tlsLock.Lock()
	defer tlsLock.Unlock()

	for at, v := range certHashes {
		if bytes.Equal(hash, v) {
			// note: seriously, this is remove(certIds, at)
			certHashes = append(certHashes[:at], certHashes[at+1:]...)
			tlsConfig.Certificates = append(tlsConfig.Certificates[:at], tlsConfig.Certificates[at+1:]...)
			tlsConfig.BuildNameToCertificate()
			return
		}
	}
}

func getCertificate(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	// with this call here, letsencrypt will do the SNI "handshake" if relevant
	cert, err := letsEncrypt.GetCertificate(clientHello)
	if cert != nil || err != nil {
		return cert, err
	}

	tlsLock.RLock()
	defer tlsLock.RUnlock()
	c := tlsConfig

	// same algorithm as tls.Config.getCertificate, but it is not public, and now we hold a lock
	if len(c.Certificates) == 0 {
		return nil, errors.New("no tls site configured")
	}

	if len(c.Certificates) == 1 || c.NameToCertificate == nil {
		// There's only one choice, so no point doing any work.
		return &c.Certificates[0], nil
	}

	name := strings.ToLower(clientHello.ServerName)
	for len(name) > 0 && name[len(name)-1] == '.' {
		name = name[:len(name)-1]
	}

	if cert, ok := c.NameToCertificate[name]; ok {
		return cert, nil
	}

	// try replacing labels in the name with wildcards until we get a
	// match.
	labels := strings.Split(name, ".")
	for i := range labels {
		labels[i] = "*"
		candidate := strings.Join(labels, ".")
		if cert, ok := c.NameToCertificate[candidate]; ok {
			return cert, nil
		}
	}

	// If nothing matches, return the first certificate.
	return &c.Certificates[0], nil
}

func maintls() {
	var err error
	config := &tls.Config{}
	config.GetCertificate = getCertificate
	if err != nil {
		log.Print(err)
		return
	}

	listener, err := net.Listen("tcp", ":443")
	if err != nil {
		var err2 error
		listener, err2 = net.Listen("tcp", ":4443")
		if err2 != nil {
			log.Print("err: ", err)
			log.Print("err: ", err2)
		}
	}
	if listener == nil {
		return
	}

	// TODO would be really nice if we could open/close 443/4443 depending on tls configured
	tlsListener := tls.NewListener(listener.(*net.TCPListener), config)
	log.Printf("http server listening on port: %s", listener.Addr())
	go http.Serve(tlsListener, http.HandlerFunc(serve))
}

func main() {
	log.SetFlags(log.Flags() | log.Lmicroseconds | log.Lshortfile)
	log.SetPrefix("lambdaroach ")

	// TODO this should be per email, per hosts, not global
	// TODO now tls generation is done on server, and saved there, perhaps better use client over admin?
	if err := letsEncrypt.CacheFile("letsencrypt.cache"); err != nil {
		log.Fatal(err)
	}
	letsEncrypt.SetHosts([]string{})

	listener, err := net.Listen("tcp", ":80")
	if err != nil {
		var err2 error
		listener, err2 = net.Listen("tcp", ":8000")
		if err2 != nil {
			log.Print("err: ", err)
			log.Panic("err: ", err2)
		}
	}
	log.Printf("http server listening on port: %s", listener.Addr())
	go http.Serve(listener, http.HandlerFunc(serve))
	maintls()
	serveAdmin()
}
