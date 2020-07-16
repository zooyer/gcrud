package gcrud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
)

// Handler
type Handler interface {
	Return(ctx *gin.Context, res interface{}, err error)
}

// handler
type handler struct{}

// Query query params.
type Query struct {
	Sort   string   `form:"sort" json:"sort,omitempty"`
	Omit   []string `form:"omit" json:"omit,omitempty"`
	Select []string `form:"select" json:"select,omitempty"`

	Page int `form:"page" json:"page,omitempty"`
	Size int `form:"size" json:"size,omitempty"`
}

// Result query restful.
type Result struct {
	Query
	Count  int         `form:"count" json:"count"`
	Total  int         `form:"total" json:"total"`
	Result interface{} `form:"result" json:"result"`
}

// omitParams omit query params.
var omitParams = make(map[string]bool)

// Default default handler.
var Default handler

// Return default return handle.
func (h handler) Return(ctx *gin.Context, res interface{}, err error) {
	if err != nil {
		ctx.String(http.StatusInternalServerError, err.Error())
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// Mount mount to db and restful api.
func (h handler) Mount(mux gin.IRouter, db *gorm.DB, model interface{}) gin.IRouter {
	return Mount(mux, db, model, h)
}

// init init omit params.
func init() {
	t := reflect.TypeOf(Query{})
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("form")
		if tag == "" {
			tag = t.Field(i).Tag.Get("json")
			tag = strings.Split(tag, ",")[0]
		}
		omitParams[tag] = true
	}
}

// where request params to sql where element.
func where(ctx *gin.Context, db *gorm.DB, model interface{}) *gorm.DB {
	db = db.New()
	var scope = db.NewScope(model)

	_ = ctx.Request.ParseForm()
	for key, val := range ctx.Request.Form {
		if omitParams[key] {
			continue
		}

		if _, ok := scope.FieldByName(key); ok && len(val) > 0 {
			if len(val) > 1 {
				db = db.Where(fmt.Sprintf("%s IN (?)", key), val)
			} else {
				db = db.Where(fmt.Sprintf("%s LIKE ?", key), fmt.Sprintf("%%%s%%", val[0]))
			}
		}
	}

	return db.Model(model)
}

// getType get type by model.
func getType(model interface{}) reflect.Type {
	modelType := reflect.TypeOf(model)
	for modelType.Kind() == reflect.Ptr {
		modelType = modelType.Elem()
	}

	return modelType
}

// getField get path field by index.
func getField(ctx *gin.Context, index int) string {
	path := strings.Trim(ctx.Request.URL.Path, "/")
	fields := strings.Split(path, "/")
	if index > 0 {
		return fields[index]
	}
	return fields[len(fields)+index]
}

// [GET] /:field/:value
func Get(ctx *gin.Context, db *gorm.DB, model interface{}) (record interface{}, err error) {
	var m = reflect.New(getType(model)).Elem()

	db = where(ctx, db, model)
	field := getField(ctx, -2)
	value := ctx.Param("value")

	if err = db.Where(field+" = ?", value).First(m.Addr().Interface()).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = nil
		}
		return
	}

	return m.Interface(), nil
}

// [GET] /?page=1&size=10&params...
func Gets(ctx *gin.Context, db *gorm.DB, model interface{}) (result *Result, err error) {
	var scope = db.NewScope(model)
	var query Query
	if err = ctx.Bind(&query); err != nil {
		return
	}

	var total int
	if err = db.Model(model).Count(&total).Error; err != nil {
		return
	}

	db = where(ctx, db, model)

	var slice = reflect.New(reflect.SliceOf(getType(model))).Elem()

	// select field
	var selected = make(map[string]bool)
	for _, s := range query.Select {
		selected[s] = true
	}

	// omit field
	if len(query.Omit) > 0 {
		if len(selected) == 0 {
			for _, field := range db.NewScope(model).Fields() {
				selected[field.DBName] = true
			}
		}
		for _, omit := range query.Omit {
			delete(selected, omit)
		}
	}

	// db select field
	if len(selected) > 0 {
		var fields = make([]string, 0, len(selected))
		for field := range selected {
			if _, ok := scope.FieldByName(field); ok {
				fields = append(fields, scope.Quote(field))
			}
		}
		db = db.Select(fields)
	}

	// sort
	if query.Sort != "" {
		db = db.Order(query.Sort, true)
	}

	// size and page
	if query.Size > 0 {
		db = db.Limit(query.Size)
		if query.Page > 0 {
			db = db.Offset((query.Page - 1) * query.Size)
		}
	}

	//for key, val := range ctx.Request.Form {
	//	if omitParams[key] {
	//		continue
	//	}
	//
	//	if _, ok := scope.FieldByName(key); ok && len(val) > 0 {
	//		db = db.Where(fmt.Sprintf("%s LIKE ?", key), fmt.Sprintf("%%%s%%", val[0]))
	//	}
	//}

	if err = db.Find(slice.Addr().Interface()).Error; err != nil {
		return
	}

	return &Result{
		Query:  query,
		Count:  slice.Len(),
		Total:  total,
		Result: slice.Interface(),
	}, nil
}

// [POST] / {}
func Post(ctx *gin.Context, db *gorm.DB, model interface{}) (record interface{}, err error) {
	var value = reflect.New(getType(model))
	if err = ctx.Bind(value.Interface()); err != nil {
		return nil, err
	}

	if err = db.Create(value.Interface()).Error; err != nil {
		return
	}

	return value.Interface(), nil
}

// [POST] / [{}]
func Posts(ctx *gin.Context, db *gorm.DB, model interface{}) (records interface{}, err error) {
	var slice = reflect.New(reflect.SliceOf(getType(model))).Elem()
	if err = ctx.Bind(slice.Addr().Interface()); err != nil {
		return
	}

	if slice.Len() == 0 {
		return
	}

	tx := db.Begin()
	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			err = tx.Commit().Error
		}
	}()

	for i := 0; i < slice.Len(); i++ {
		if err = tx.Create(slice.Index(i).Addr().Interface()).Error; err != nil {
			return
		}
	}

	return slice.Interface(), nil
}

// [PUT] /:field/:value {}
func Put(ctx *gin.Context, db *gorm.DB, model interface{}) (affected int64, err error) {
	var m map[string]interface{}
	if err = ctx.Bind(&m); err != nil {
		return
	}

	db = where(ctx, db, model)
	field := getField(ctx, -2)
	value := ctx.Param("value")

	db = db.Where(field+" = ?", value).Updates(m)

	return db.RowsAffected, db.Error
}

// [PUT] /$field [{"id":1}]
func Puts(ctx *gin.Context, db *gorm.DB, model interface{}) (affected int64, err error) {
	var values []map[string]interface{}
	if err = ctx.Bind(&values); err != nil {
		return
	}

	if len(values) == 0 {
		return
	}

	tx := db.Begin()
	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			tx = tx.Commit()
			affected = tx.RowsAffected
			err = tx.Error
		}
	}()

	tx = where(ctx, tx, model)
	field := getField(ctx, -1)

	for _, value := range values {
		v := value[field]
		delete(value, field)
		if err = tx.Where(field+" = ?", v).Updates(value).Error; err != nil {
			return
		}
	}

	return
}

// [DELETE] /:field/:value
func Delete(ctx *gin.Context, db *gorm.DB, model interface{}) (affected int64, err error) {
	db = where(ctx, db, model)
	field := getField(ctx, -2)
	value := ctx.Param("value")

	db = db.Where(field+" = ?", value).Delete(model)

	return db.RowsAffected, db.Error
}

// [DELETE] /:field []
func Deletes(ctx *gin.Context, db *gorm.DB, model interface{}) (affected int64, err error) {
	var values []interface{}
	if err = ctx.Bind(&values); err != nil {
		return
	}

	if len(values) == 0 {
		return
	}

	db = where(ctx, db, model)
	field := getField(ctx, -1)

	db = db.Where(field+" IN (?)", values).Delete(model)

	return db.RowsAffected, db.Error
}

// Mount mount model to db and restful api.
func Mount(mux gin.IRouter, db *gorm.DB, model interface{}, handler ...Handler) gin.IRouter {
	var h Handler = Default
	if len(handler) > 0 {
		h = handler[0]
	}

	get := func(ctx *gin.Context) {
		record, err := Get(ctx, db, model)
		h.Return(ctx, record, err)
	}

	gets := func(ctx *gin.Context) {
		result, err := Gets(ctx, db, model)
		h.Return(ctx, result, err)
	}

	post := func(ctx *gin.Context) {
		record, err := Post(ctx, db, model)
		h.Return(ctx, record, err)
	}

	posts := func(ctx *gin.Context) {
		records, err := Posts(ctx, db, model)
		h.Return(ctx, records, err)
	}

	put := func(ctx *gin.Context) {
		affected, err := Put(ctx, db, model)
		h.Return(ctx, affected, err)
	}

	puts := func(ctx *gin.Context) {
		affected, err := Puts(ctx, db, model)
		h.Return(ctx, affected, err)
	}

	del := func(ctx *gin.Context) {
		affected, err := Delete(ctx, db, model)
		h.Return(ctx, affected, err)
	}

	deletes := func(ctx *gin.Context) {
		affected, err := Deletes(ctx, db, model)
		h.Return(ctx, affected, err)
	}

	// generate restful api
	var scope = db.NewScope(model)
	for _, f := range scope.Fields() {
		for _, name := range []string{f.Name, f.DBName} {
			field := mux.Group(name)
			{
				field.PUT("", puts)
				field.DELETE("", deletes)
			}

			value := field.Group("/:value")
			{
				value.GET("", get)
				value.PUT("", put)
				value.DELETE("", del)
			}

			if f.Name == f.DBName {
				break
			}
		}
	}

	// generate batch restful api
	mux.GET("", gets)
	mux.POST("", func(ctx *gin.Context) {
		data, err := ioutil.ReadAll(ctx.Request.Body)
		if err != nil {
			h.Return(ctx, nil, err)
			return
		}
		defer ctx.Request.Body.Close()

		reader := ioutil.NopCloser(bytes.NewReader(data))
		ctx.Request.Body = reader
		ctx.Request.GetBody = func() (closer io.ReadCloser, err error) {
			return reader, nil
		}

		var v interface{}
		if err = json.Unmarshal(data, &v); err != nil {
			h.Return(ctx, nil, err)
			return
		}

		if _, ok := v.([]interface{}); ok {
			posts(ctx)
			return
		}

		post(ctx)
	})

	return mux
}
