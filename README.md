# gopostfile [![builds.sr.ht status](https://builds.sr.ht/~delthas/gopostfile.svg)](https://builds.sr.ht/~delthas/gopostfile?)

A small HTTP server to let users upload files with an HTTP POST, with MySQL user authentication.

Setup:
- copy `gopostfile.example.yml` and edit it
- put `gopostfile` somewhere it can be run by everyone (or set `copy_exe` in the config)
- run `gopostfile -config gopostfile.yml` as root

Usage:

Either of:
- POST on `/desired/path/to/file`, with the raw file data in the request body
- POST on `/`, with a multipart form containing the file; **the part filename must be the desired path to the file**

On success, the response will be 200, `text/plain`, containing a link to the uploaded file.

| OS | URL |
|---|---|
| Linux x64 | https://delthas.fr/gopostfile/linux/gopostfile |
| Mac OS X x64 | https://delthas.fr/gopostfile/mac/gopostfile |
| Windows x64 | Not compatible with Windows |
