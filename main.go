package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
	"gopkg.in/yaml.v2"
)

type config struct {
	Port int `yaml:"port"`
	Ftp  struct {
		Host    string `yaml:"host"`
		Port    int    `yaml:"port"`
		Timeout int    `yaml:"timeout"`
	} `yaml:"ftp"`
	Urls []struct {
		Path  string         `yaml:"path"`
		Url   string         `yaml:"url"`
		Regex *regexp.Regexp `yaml:"-"`
	} `yaml:"urls"`
}

func fatal(f string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, f, args...)
	os.Exit(1)
}

func e(f string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, f+"\n", args...)
}

func percentCheck(template string) error {
	escape := false
	for _, r := range template {
		if r == '%' {
			escape = !escape
		}
	}
	if escape {
		return fmt.Errorf("parse percent template: unterminated escape sequence in: %s", template)
	}
	return nil
}

func percentTemplate(template string, replace func(match string) (string, error)) (string, error) {
	var sb strings.Builder
	last := 0
	escape := false
	for i, r := range template {
		if r == '%' {
			if escape {
				match := template[last:i]
				var rep string
				if match == "" {
					rep = "%"
				} else {
					var err error
					rep, err = replace(match)
					if err != nil {
						return "", err
					}
				}
				sb.WriteString(rep)
			} else {
				sb.WriteString(template[last:i])
			}
			escape = !escape
			last = i + 1
		}
	}
	if escape {
		return "", fmt.Errorf("template replace: unterminated escape sequence in: %s", template)
	}
	sb.WriteString(template[last:])
	return sb.String(), nil
}

func main() {
	configPath := flag.String("config", "gopostfile.yml", "config file path")
	flag.Parse()

	f, err := os.Open(*configPath)
	if err != nil {
		fatal("error: open config file: %s", err.Error())
		return
	}

	var config config
	err = yaml.NewDecoder(f).Decode(&config)
	if err != nil {
		fatal("error: decode config file: %s", err.Error())
		return
	}

	for i, v := range config.Urls {
		if err := percentCheck(v.Url); err != nil {
			fatal("error: parse url replace: %s", err.Error())
			return
		}
		r, err := regexp.Compile(v.Path)
		if err != nil {
			fatal("error: compile regex %s: %s", v.Path, err.Error())
			return
		}
		config.Urls[i].Regex = r
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Body == http.NoBody {
			w.WriteHeader(400)
			return
		}
		defer r.Body.Close()
		if r.Method != "POST" {
			w.WriteHeader(400)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok {
			w.WriteHeader(403)
			return
		}

		c, err := ftp.Dial(net.JoinHostPort(config.Ftp.Host, strconv.Itoa(config.Ftp.Port)), ftp.DialWithTimeout(time.Duration(config.Ftp.Timeout)*time.Second))
		if err != nil {
			e("error: connect to ftp: %s", err.Error())
			w.WriteHeader(500)
			return
		}

		err = c.Login(user, pass)
		if err != nil {
			e("error: login to ftp: %s", err.Error())
			w.WriteHeader(403)
			return
		}
		defer c.Quit()

		var reader io.Reader
		var path string

		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			r, err := r.MultipartReader()
			if err != nil {
				e("error: open multipart reader: %s", err)
				w.WriteHeader(400)
				return
			}
			for {
				part, err := r.NextPart()
				if err != nil {
					e("error: get multipart next part: %s", err)
					w.WriteHeader(400)
					return
				}
				path = strings.TrimLeft(part.FileName(), "/")
				if path == "" {
					continue
				}
				reader = part
				break
			}
		} else {
			path = strings.TrimLeft(r.URL.Path, "/")
			if path == "" {
				w.WriteHeader(400)
				return
			}
			reader = r.Body
		}

		if err := c.Stor(path, reader); err != nil {
			if ee, ok := err.(*textproto.Error); ok && ee.Code == 550 && len(path) > 1 {
				// no such file or folder, try to create parent directories
				// reader has not been read yet
				i := 1
				for {
					j := strings.IndexRune(path[i:], '/')
					if j == -1 {
						break
					}
					i = j + i + 1
					err = c.MakeDir(path[:i])
					if err != nil {
						if ee, ok := err.(*textproto.Error); !ok || ee.Code != 550 {
							w.WriteHeader(400)
							return
						}
					}
				}
				if err := c.Stor(path, reader); err != nil {
					e("error: stor file to ftp at path %s: %s", path, err)
					w.WriteHeader(500)
					return
				}
			} else {
				e("error: stor file to ftp at path %s: %s", path, err)
				w.WriteHeader(500)
				return
			}
		}

		if !strings.HasPrefix(path, "/") {
			cwd, err := c.CurrentDir()
			if err != nil {
				e("error: get cwd: %s", err)
				w.WriteHeader(500)
				return
			}
			path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(cwd + "/" + path)))
		}

		url := ""
		for _, v := range config.Urls {
			groups := v.Regex.FindStringSubmatch(path)
			if groups == nil {
				continue
			}
			u, err := percentTemplate(v.Url, func(match string) (string, error) {
				switch match {
				case "user":
					return user, nil
				case "password":
					return pass, nil
				default:
					i, err := strconv.Atoi(match)
					if err != nil {
						return "", err
					}
					if i >= len(groups) {
						return "", nil
					}
					return groups[i], nil
				}
			})
			if err != nil {
				e("error: execute template url %s: %s", v.Url, err)
				w.WriteHeader(500)
				return
			}
			url = u
			break
		}

		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(url))
	})
	fmt.Println("listening on localhost:" + strconv.Itoa(config.Port))
	log.Fatal(http.ListenAndServe(":"+strconv.Itoa(config.Port), nil))
}
