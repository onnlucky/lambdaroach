all: roachctl lambdaroach

roachctl: client/main.go
	go build -o $@ $^

lambdaroach: server/admin.go server/main.go
	go build -o $@ $^

PREFIX?=/usr/local
BINDIR:=$(DESTDIR)$(PREFIX)/bin
install: roachctl lambdaroach
	mkdir -p $(BINDIR)
	cp $^ $(BINDIR)/

test: roachctl lambdaroach
	go test ./...
	sh -c "./lambdaroach & sleep 1; ./roachctl -d example -h localhost; sleep 1; curl localhost:8000; sleep 1; kill %1"

clean:
	rm -rf roachctl lambdaroach

.PHONY: all install test clean
