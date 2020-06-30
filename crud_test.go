package gcrud

import (
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"github.com/mattn/go-sqlite3"
	"testing"
)

var _ sqlite3.SQLiteDriver

func TestMount(t *testing.T) {
	type Test struct {
		gorm.Model
		Name string
		Age  int
	}

	db, err := gorm.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	db.AutoMigrate(&Test{})

	engine := gin.Default()
	Mount(engine, db, "/test", &Test{})
	t.Fatal(engine.Run())
}
