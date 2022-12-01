# gopostfile [![builds.sr.ht status](https://builds.sr.ht/~delthas/gopostfile.svg)](https://builds.sr.ht/~delthas/gopostfile?)

A small HTTP server to let users upload files with an HTTP POST, proxying to an FTP server.

Setup:
- copy `gopostfile.example.yml` and edit it
- run `gopostfile -config gopostfile.yml`

Usage:

Either of:
- POST on `/desired/path/to/file`, with the raw file data in the request body
- POST on `/`, with a multipart form containing the file; **the part filename must be the desired path to the file**

On success, the response will be 201, with the Location header containing a URL to the uploaded file.

| OS | URL |
|---|---|
| Linux x64 | https://delthas.fr/gopostfile/linux/gopostfile |
| Mac OS X x64 | https://delthas.fr/gopostfile/mac/gopostfile |
| Windows x64 | https://delthas.fr/gopostfile/windows/gopostfile.exe |
