// +build go1.8

package http

import (
	"flag"
	"io/ioutil"
	"net"
	"net/http"
	"path"
	"strings"
	"testing"
	"time"

	_ "github.com/ncw/rclone/backend/local"
	"github.com/ncw/rclone/cmd/serve/httplib"
	"github.com/ncw/rclone/fs"
	"github.com/ncw/rclone/fs/config"
	"github.com/ncw/rclone/fs/filter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	updateGolden = flag.Bool("updategolden", false, "update golden files for regression test")
	httpServer   *server
)

const (
	testBindAddress = "localhost:51777"
	testURL         = "http://" + testBindAddress + "/"
)

func startServer(t *testing.T, f fs.Fs) {
	opt := httplib.DefaultOpt
	opt.ListenAddr = testBindAddress
	httpServer = newServer(f, &opt)
	go httpServer.serve()

	// try to connect to the test server
	pause := time.Millisecond
	for i := 0; i < 10; i++ {
		conn, err := net.Dial("tcp", testBindAddress)
		if err == nil {
			_ = conn.Close()
			return
		}
		// t.Logf("couldn't connect, sleeping for %v: %v", pause, err)
		time.Sleep(pause)
		pause *= 2
	}
	t.Fatal("couldn't connect to server")

}

func TestInit(t *testing.T) {
	// Configure the remote
	config.LoadConfig()
	// fs.Config.LogLevel = fs.LogLevelDebug
	// fs.Config.DumpHeaders = true
	// fs.Config.DumpBodies = true

	// exclude files called hidden.txt and directories called hidden
	require.NoError(t, filter.Active.AddRule("- hidden.txt"))
	require.NoError(t, filter.Active.AddRule("- hidden/**"))

	// Create a test Fs
	f, err := fs.NewFs("testdata/files")
	require.NoError(t, err)

	startServer(t, f)
}

// check body against the file, or re-write body if -updategolden is
// set.
func checkGolden(t *testing.T, fileName string, got []byte) {
	if *updateGolden {
		t.Logf("Updating golden file %q", fileName)
		err := ioutil.WriteFile(fileName, got, 0666)
		require.NoError(t, err)
	} else {
		want, err := ioutil.ReadFile(fileName)
		require.NoError(t, err)
		wants := strings.Split(string(want), "\n")
		gots := strings.Split(string(got), "\n")
		assert.Equal(t, wants, gots, fileName)
	}
}

func TestGET(t *testing.T) {
	for _, test := range []struct {
		URL    string
		Status int
		Golden string
		Method string
		Range  string
	}{
		{
			URL:    "",
			Status: http.StatusOK,
			Golden: "testdata/golden/index.html",
		},
		{
			URL:    "notfound",
			Status: http.StatusNotFound,
			Golden: "testdata/golden/notfound.html",
		},
		{
			URL:    "dirnotfound/",
			Status: http.StatusNotFound,
			Golden: "testdata/golden/dirnotfound.html",
		},
		{
			URL:    "hidden/",
			Status: http.StatusNotFound,
			Golden: "testdata/golden/hiddendir.html",
		},
		{
			URL:    "one%25.txt",
			Status: http.StatusOK,
			Golden: "testdata/golden/one.txt",
		},
		{
			URL:    "hidden.txt",
			Status: http.StatusNotFound,
			Golden: "testdata/golden/hidden.txt",
		},
		{
			URL:    "three/",
			Status: http.StatusOK,
			Golden: "testdata/golden/three.html",
		},
		{
			URL:    "three/a.txt",
			Status: http.StatusOK,
			Golden: "testdata/golden/a.txt",
		},
		{
			URL:    "",
			Method: "HEAD",
			Status: http.StatusOK,
			Golden: "testdata/golden/indexhead.txt",
		},
		{
			URL:    "one%25.txt",
			Method: "HEAD",
			Status: http.StatusOK,
			Golden: "testdata/golden/onehead.txt",
		},
		{
			URL:    "",
			Method: "POST",
			Status: http.StatusMethodNotAllowed,
			Golden: "testdata/golden/indexpost.txt",
		},
		{
			URL:    "one%25.txt",
			Method: "POST",
			Status: http.StatusMethodNotAllowed,
			Golden: "testdata/golden/onepost.txt",
		},
		{
			URL:    "two.txt",
			Status: http.StatusOK,
			Golden: "testdata/golden/two.txt",
		},
		{
			URL:    "two.txt",
			Status: http.StatusPartialContent,
			Range:  "bytes=2-5",
			Golden: "testdata/golden/two2-5.txt",
		},
		{
			URL:    "two.txt",
			Status: http.StatusPartialContent,
			Range:  "bytes=0-6",
			Golden: "testdata/golden/two-6.txt",
		},
		{
			URL:    "two.txt",
			Status: http.StatusPartialContent,
			Range:  "bytes=3-",
			Golden: "testdata/golden/two3-.txt",
		},
	} {
		method := test.Method
		if method == "" {
			method = "GET"
		}
		req, err := http.NewRequest(method, testURL+test.URL, nil)
		require.NoError(t, err)
		if test.Range != "" {
			req.Header.Add("Range", test.Range)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, test.Status, resp.StatusCode, test.Golden)
		body, err := ioutil.ReadAll(resp.Body)
		require.NoError(t, err)

		checkGolden(t, test.Golden, body)
	}
}

type mockNode struct {
	path  string
	isdir bool
}

func (n mockNode) Path() string { return n.path }
func (n mockNode) Name() string {
	if n.path == "" {
		return ""
	}
	return path.Base(n.path)
}
func (n mockNode) IsDir() bool { return n.isdir }

func TestAddEntry(t *testing.T) {
	var es entries
	es.addEntry(mockNode{path: "", isdir: true})
	es.addEntry(mockNode{path: "dir", isdir: true})
	es.addEntry(mockNode{path: "a/b/c/d.txt", isdir: false})
	es.addEntry(mockNode{path: "a/b/c/colon:colon.txt", isdir: false})
	es.addEntry(mockNode{path: "\"quotes\".txt", isdir: false})
	assert.Equal(t, entries{
		{remote: "", URL: "/", Leaf: "/"},
		{remote: "dir", URL: "dir/", Leaf: "dir/"},
		{remote: "a/b/c/d.txt", URL: "d.txt", Leaf: "d.txt"},
		{remote: "a/b/c/colon:colon.txt", URL: "./colon:colon.txt", Leaf: "colon:colon.txt"},
		{remote: "\"quotes\".txt", URL: "%22quotes%22.txt", Leaf: "\"quotes\".txt"},
	}, es)
}

func TestFinalise(t *testing.T) {
	httpServer.srv.Close()
}
