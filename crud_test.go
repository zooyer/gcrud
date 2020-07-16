package gcrud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/mattn/go-sqlite3"
)

var _ sqlite3.SQLiteDriver

type (
	people struct {
		gorm.Model
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
)

var (
	db     *gorm.DB
	engine = gin.Default()
	done   = make(chan struct{})
)

func init() {
	var err error
	if db, err = gorm.Open("sqlite3", ":memory:"); err != nil {
		panic(err)
	}

	//db = db.Debug()

	db.AutoMigrate(&people{})
	var peoples = []people{
		{Name: "z1", Age: 12},
		{Name: "bzh", Age: 19},
		{Name: "z1", Age: 21},
		{Name: "abc", Age: 11},
		{Name: "zzz", Age: 25},
	}

	for _, p := range peoples {
		if err := db.Create(&p).Error; err != nil {
			panic(err)
		}
	}

	engine.GET("/stop", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, "stopping...")
		time.AfterFunc(time.Second, func() {
			os.Exit(0)
		})
	})

	go func() {
		panic(engine.Run())
	}()
}

func newReq(method, uri string, data interface{}, params ...string) (*http.Request, error) {
	var body io.Reader
	if data != nil {
		data, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}

	u, err := url.Parse("http://127.0.0.1:8080" + uri)
	if err != nil {
		return nil, err
	}

	query := u.Query()
	for i := 0; i < len(params); i += 2 {
		query.Add(params[i], params[i+1])
	}
	u.RawQuery = query.Encode()

	req, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		return nil, err
	}

	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return req, nil
}

func do(req *http.Request) (res string, status int, err error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	return string(data), resp.StatusCode, nil
}

func request(method, uri string, data interface{}, params ...string) (string, error) {
	req, err := newReq(method, uri, data, params...)
	if err != nil {
		return "", err
	}

	res, status, err := do(req)
	if err != nil {
		return "", err
	}

	if status != http.StatusOK {
		return res, fmt.Errorf("response status: %v", status)
	}

	return res, nil
}

func get(uri string, params ...string) (string, error) {
	return request("GET", uri, nil, params...)
}

func post(uri string, data interface{}, params ...string) (string, error) {
	return request("POST", uri, data, params...)
}

func put(uri string, data interface{}, params ...string) (string, error) {
	return request("PUT", uri, data, params...)
}

func del(uri string, data interface{}, params ...string) (string, error) {
	return request("DELETE", uri, data, params...)
}

func stop(t *testing.T) {
	if _, err := get("/stop"); err != nil {
		t.Fatal(err)
	}
	close(done)
}

func TestMount(t *testing.T) {
	assert := func(res string, err error) {
		if err != nil {
			t.Fatal(err)
		}
		t.Log(res)
	}
	p := engine.Group("/people")
	Mount(p, db, &people{})

	go func() {
		assert(post("/people", people{Name: "a", Age: 1}))
		assert(post("/people", []people{{Name: "a", Age: 1}, {Name: "b", Age: 2}}))
		assert(del("/people/id/1", nil))
		assert(del("/people/id", []int{1, 2}))
		assert(put("/people/id/3", map[string]interface{}{"name": "c", "age": 3}))
		assert(put("/people/id", []map[string]interface{}{{"name": "d", "age": 4}, {"name": "e", "age": 5}}))
		assert(get("/people/id/5"))
		assert(get("/people"))
		stop(t)
	}()
	<-done
}
