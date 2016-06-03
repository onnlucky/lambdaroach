# lambdaroach

Upload and host apps inside the cockroachdb. Think AWS Lambda with roachdbs scalability and survivability.

* http://cockroachlabs.com

# Status: Readme Driven Development

At the moment it is a standalone go http server that can receive apps over an administrative connection. On first requests,
it will launch the app and keep it running. When new versions are uploaded, the old instances bleed out, and new requests are
served from the new instances.

# Example

`app/index.html`
```html
<title>Hello World!</title>
<p>Welcome to lambdaroach ...</p>
```
`app/lambda.config.json`
```json
{
  "name":"test",
  "hostname":"example.com",
  "command":"python -m SimpleHTTPServer ${PORT}"
}
```

And upload it to a localhost staging server
```
app $ lambdaroachserver
http server listening on port: [::]:8000
...
app $ lambdaroach -h localhost
...
uploaded files: 2 bytes: 132
ok
app $ curl http://localhost:8000
<title>Hello World!</title>
<p>Welcome to lambdaroach ...</p>
```

App servers must either pick up the port to listen to from the PORT environment variable, or the system will replace any
occurance of `${PORT}` in the `command` config.


# Future

The plan is to run this alongside or inside cockroachdb, and use its gossip and knowledge of peers to distribute the apps
to all servers. And to allow apps to connect to "their" database over localhost.

And to integrate the http server statistics in cockroachdbs status page, as well as pushing app "event" in the database, like
errors or other diagnostics.

When actually running apps for scale, you will need to expose all servers through tcp or http loadbalacers.
