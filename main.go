package main

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/go-sql-driver/mysql"
)

const unauthorized = 127
const writeMagic = "@@WRITE@@FILE@@"

type config struct {
	Port      int    `yaml:"port"`
	Path      string `yaml:"path"`
	UidOffset int    `yaml:"uid_offset"`
	Url       string `yaml:"url"`
	CopyExe   bool   `yaml:"copy_exe"`
	Sql       struct {
		Db       string `yaml:"db"`
		User     string `yaml:"user"`
		Password string `yaml:"password"`
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		Request  string `yaml:"request"`
	} `yaml:"sql"`
}

func fatal(f string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, f, args...)
	os.Exit(1)
}

func e(f string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, f, args...)
}

// adapted from ioutil.TempFile
func tempFile(dir, pattern string, mode os.FileMode) (f *os.File, err error) {
	if dir == "" {
		dir = os.TempDir()
	}

	var prefix, suffix string
	if pos := strings.LastIndex(pattern, "*"); pos != -1 {
		prefix, suffix = pattern[:pos], pattern[pos+1:]
	} else {
		prefix = pattern
	}

	for i := 0; i < 10000; i++ {
		name := filepath.Join(dir, prefix+strconv.Itoa(int(1e9 + rand.Int31()%1e9))[1:]+suffix)
		f, err = os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
		if os.IsExist(err) {
			continue
		}
		break
	}
	return
}

func main() {
	if len(os.Args) == 3 && os.Args[1] == writeMagic {
		write(os.Args[2])
		return
	}
	server()
}

func write(path string) {
	err := os.MkdirAll(filepath.Dir(path), 0777)
	if err != nil {
		e("error: make dir for path %s: %s", path, err)
		os.Exit(unauthorized)
		return
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		e("error: open file at path %s: %s", path, err)
		os.Exit(unauthorized)
		return
	}
	_, err = io.Copy(f, os.Stdin)
	cerr := f.Close()
	if cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		e("error: copy to file at path %s: %s", path, err)
		os.Exit(1)
		return
	}
}

func server() {
	rand.Seed(time.Now().UnixNano())

	configPath := flag.String("config", "gopostfile.yml", "config file path")
	flag.Parse()

	exePath, err := os.Executable()
	if err != nil {
		fatal("error: get executable path: %s", err.Error())
		return
	}

	f, err := os.Open(*configPath)
	if err != nil {
		fatal("error: open config file: %s", err.Error())
		return
	}

	var config config
	err = yaml.NewDecoder(f).Decode(&config)
	if err != nil {
		fatal("error: could not decode config file: %s", err.Error())
		return
	}

	if config.CopyExe {
		exeCopy, err := tempFile("/tmp", "gopostfile-", 0755)
		if err != nil {
			fatal("error: open temp file for exe copy: %s", err.Error())
			return
		}
		defer os.Remove(exeCopy.Name())

		f, err := os.Open(exePath)
		if err != nil {
			exeCopy.Close()
			fatal("error: open self executable: %s", err.Error())
			return
		}

		_, err = io.Copy(exeCopy, f)
		cerr := exeCopy.Close()
		f.Close()
		if cerr != nil && err == nil {
			err = cerr
		}
		if err != nil {
			e("error: copy self to file at path %s: %s", exeCopy.Name(), err)
			os.Exit(1)
			return
		}

		f, err = os.Open(*configPath)
		if err != nil {
			fatal("error: open config file: %s", err.Error())
			return
		}

		exePath = exeCopy.Name()
	}

	mysqlConfig := mysql.NewConfig()
	mysqlConfig.User = config.Sql.User
	mysqlConfig.Passwd = config.Sql.Password
	mysqlConfig.DBName = config.Sql.Db
	mysqlConfig.Net = "tcp"
	mysqlConfig.Addr = net.JoinHostPort(config.Sql.Host, strconv.Itoa(config.Sql.Port))
	db, err := sql.Open("mysql", mysqlConfig.FormatDSN())
	if err != nil {
		fatal("error: could not open connection to database: %s", err.Error())
		return
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		fatal("error: could not send initial ping to database: %s", err.Error())
		return
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
		var uid int
		err := db.QueryRow(config.Sql.Request, user, pass).Scan(&uid)
		if err == sql.ErrNoRows {
			w.WriteHeader(403)
			return
		}
		if err != nil {
			e("error: query mysql user: %s", err)
			w.WriteHeader(500)
			return
		}
		uid += config.UidOffset

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

		filePath := filepath.Join(config.Path, path)

		cmd := exec.Command(exePath, writeMagic, filePath)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{
				Uid:         uint32(uid),
				Gid:         uint32(uid),
				NoSetGroups: true,
			},
		}
		cmd.Stdin = reader
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			e("error: fork self for writing: %s", err)
			w.WriteHeader(500)
			return
		}
		err = cmd.Wait()
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == unauthorized {
			w.WriteHeader(403)
			return
		} else if err != nil {
			e("error: fork self exit: %s", err)
			w.WriteHeader(500)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(config.Url + path))
	})
	fmt.Println("listening on localhost:" + strconv.Itoa(config.Port))
	log.Fatal(http.ListenAndServe(":"+strconv.Itoa(config.Port), nil))
}
