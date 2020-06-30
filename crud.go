package crud

import (
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
)

// 查询参数
type Query struct {
	Sort   string   `form:"sort" json:"sort,omitempty"`
	Omit   []string `form:"omit" json:"omit,omitempty"`
	Select []string `form:"select" json:"select,omitempty"`

	Page int `form:"page" json:"page,omitempty"`
	Size int `form:"size" json:"size,omitempty"`
}

// 查询结果
type Result struct {
	Query
	Count  int         `form:"count" json:"count"`
	Total  int         `form:"total" json:"total"`
	Result interface{} `form:"result" json:"result"`
}

var omitParams = make(map[string]bool)

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

func getType(model interface{}) reflect.Type {
	modelType := reflect.TypeOf(model)
	for modelType.Kind() == reflect.Ptr {
		modelType = modelType.Elem()
	}

	return modelType
}

// 从uri中获取参数id
func getID(ctx *gin.Context) (id uint, err error) {
	param := ctx.Param("id")
	i, err := strconv.Atoi(param)
	if err != nil {
		return
	}

	return uint(i), nil
}

// 获取单个 [GET] /$model/:id
func Get(ctx *gin.Context, db *gorm.DB, model interface{}) (record interface{}, err error) {
	id, err := getID(ctx)
	if err != nil {
		return
	}

	var value = reflect.New(getType(model)).Elem()

	if err = db.Model(model).Where("id = ?", id).First(value.Addr().Interface()).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = nil
		}
		return
	}

	return value.Interface(), nil
}

// 获取多个 [GET] /$model?page=1&size=10&params...
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

	for key, val := range ctx.Request.Form {
		if omitParams[key] {
			continue
		}

		if _, ok := scope.FieldByName(key); ok && len(val) > 0 {
			db = db.Where(fmt.Sprintf("%s LIKE ?", key), fmt.Sprintf("%%%s%%", val[0]))
		}
	}

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

// 新增单个 [POST] /name/ {}
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

// 新增多个 [POST] /name [{}]
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

// 更新单个 [PUT] /name/:id {}
func Put(ctx *gin.Context, db *gorm.DB, model interface{}) (err error) {
	id, err := getID(ctx)
	if err != nil {
		return
	}

	var value map[string]interface{}
	if err = ctx.Bind(&value); err != nil {
		return
	}

	if err = db.Model(model).Where("id = ?", id).Updates(value).Error; err != nil {
		return
	}

	return
}

// 更新多个 [PUT] /name [{"id":1}]
func Puts(ctx *gin.Context, db *gorm.DB, model interface{}) (err error) {
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
			err = tx.Commit().Error
		}
	}()

	for _, value := range values {
		if err = db.Model(model).Where("id = ?", value["id"]).Updates(value).Error; err != nil {
			return
		}
	}

	return
}

// 删除单个 [DELETE] /name/:id
func Delete(ctx *gin.Context, db *gorm.DB, model interface{}) (err error) {
	id, err := getID(ctx)
	if err != nil {
		return
	}

	if db = db.Model(model).Where("id = ?", id).Delete(model); db.Error != nil {
		return db.Error
	}

	//if db.RowsAffected != 1 {
	//	return fmt.Errorf("delete rows not 1, affected: %d", db.RowsAffected)
	//}

	return
}

// 删除多个 [DELETE] /name [id]
func Deletes(ctx *gin.Context, db *gorm.DB, model interface{}) (err error) {
	var id []uint
	if err = ctx.Bind(&id); err != nil {
		return
	}

	if len(id) == 0 {
		return
	}

	if db = db.Model(model).Where("id IN (?)", id).Delete(model); db.Error != nil {
		return db.Error
	}

	//if db.RowsAffected != int64(len(id)) {
	//	return fmt.Errorf("delete row:%d != affected:%d", len(id), db.RowsAffected)
	//}

	return
}

func doReturn(ctx *gin.Context, res interface{}, err error) {
	if err != nil {
		ctx.String(http.StatusInternalServerError, err.Error())
		return
	}
	ctx.JSON(http.StatusOK, res)
}

func Mount(mux gin.IRouter, db *gorm.DB, name string, model interface{}) gin.IRouter {
	name = "/" + strings.TrimLeft(name, "/")

	// 获取单个
	get := func(ctx *gin.Context) {
		record, err := Get(ctx, db, model)
		doReturn(ctx, record, err)
	}

	// 获取多个
	gets := func(ctx *gin.Context) {
		result, err := Gets(ctx, db, model)
		doReturn(ctx, result, err)
	}

	// 创建单个
	post := func(ctx *gin.Context) {
		record, err := Post(ctx, db, model)
		doReturn(ctx, record, err)
	}

	// 创建多个
	posts := func(ctx *gin.Context) {
		records, err := Posts(ctx, db, model)
		doReturn(ctx, records, err)
	}

	// 更新单个
	put := func(ctx *gin.Context) {
		err := Put(ctx, db, model)
		doReturn(ctx, nil, err)
	}

	// 更新多个
	puts := func(ctx *gin.Context) {
		err := Puts(ctx, db, model)
		doReturn(ctx, nil, err)
	}

	// 删除单个
	del := func(ctx *gin.Context) {
		err := Delete(ctx, db, model)
		doReturn(ctx, nil, err)
	}

	// 删除多个
	deletes := func(ctx *gin.Context) {
		err := Deletes(ctx, db, model)
		doReturn(ctx, nil, err)
	}

	batch := mux.Group("/batch")
	{
		group := batch.Group(name)
		{
			group.GET("", gets)
			group.POST("", posts)
			group.PUT("", puts)
			group.DELETE("", deletes)
		}
	}

	group := mux.Group(name)
	{
		group.GET("/:id", get)
		group.POST("", post)
		group.PUT("/:id", put)
		group.DELETE("/:id", del)
	}

	return group
}
