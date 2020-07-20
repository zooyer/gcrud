package gcrud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"io"
	"io/ioutil"
	"net/http"
	"reflect"
	"strings"
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

const (
	// context key in db scope.
	keyContext = "context"

	// value key in url params.
	keyParamValue = "value"
)

// omitParams omit query params.
var omitParams = make(map[string]bool)

// Default default handler.
var Default handler

var (
	// ctxType ctx type from gin context.
	ctxType = reflect.TypeOf(&gin.Context{})

	// errType err type from error.
	errType = reflect.TypeOf((*error)(nil)).Elem()
)

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

// Where request params to sql where element.
func Where(ctx *gin.Context, db *gorm.DB, model interface{}, field ...int) *gorm.DB {
	db = db.New().Set(keyContext, ctx)
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

	if len(field) > 0 && field[0] < 0 {
		field := GetField(ctx, field[0])
		value := GetValue(ctx)
		db = db.Where(field+" = ?", value)
	}

	return db.Model(reflect.New(GetType(model)).Interface())
}

// useDB set ctx in db.
func useDB(ctx context.Context, db *gorm.DB) *gorm.DB {
	return db.Set(keyContext, ctx)
}

// GetContext get context from db.
func GetContext(scope *gorm.Scope) context.Context {
	if v, exists := scope.Get(keyContext); exists {
		if ctx, ok := v.(context.Context); ok {
			return ctx
		}
	}
	return context.Background()
}

// getType get type by model.
func GetType(model interface{}) reflect.Type {
	modelType := reflect.TypeOf(model)
	for modelType.Kind() == reflect.Ptr {
		modelType = modelType.Elem()
	}

	return modelType
}

// GetField get path field by index.
func GetField(ctx *gin.Context, index int) string {
	path := strings.Trim(ctx.Request.URL.Path, "/")
	fields := strings.Split(path, "/")
	if index > 0 {
		return fields[index]
	}
	return fields[len(fields)+index]
}

// GetValue get value by uri param.
func GetValue(ctx *gin.Context) string {
	return ctx.Param(keyParamValue)
}

// BindHook get model hook.
func BindHook(model interface{}, name string) (this, hook reflect.Value, exists bool) {
	this = reflect.New(GetType(model))
	if hook = this.MethodByName(name); hook.IsValid() && !hook.IsNil() {
		exists = true
	}
	return
}

// CallHook bind hook request params and call hook function from model.
func CallHook(ctx *gin.Context, this, hook reflect.Value, model interface{}, batch ...bool) (bind reflect.Value, err error) {
	// 检查入参个数
	var in1, in2 []reflect.Value
	if t := hook.Type(); t.NumIn() > 0 {
		// 传入gin.Context参数
		if t.In(0) == ctxType {
			in1 = append(in1, reflect.ValueOf(ctx))
		}

		// 传入dto请求参数
		if inType := t.In(t.NumIn() - 1); inType != ctxType {
			var inValue reflect.Value

			// 请求参数
			if len(batch) > 0 && batch[0] {
				inValue = reflect.New(reflect.SliceOf(inType)).Elem()
			} else {
				inValue = reflect.New(inType).Elem()
			}

			// 绑定请求参数
			if err = ctx.Bind(inValue.Addr().Interface()); err != nil {
				return
			}

			// 分解成hook函数参数
			if inValue.Kind() == reflect.Slice {
				for i := 0; i < inValue.Len(); i++ {
					in2 = append(in2, inValue.Index(i))
				}
			} else {
				in2 = append(in2, inValue)
			}
		}
	}

	// 创建bind结构
	if len(batch) > 0 && batch[0] {
		bind = reflect.New(reflect.SliceOf(GetType(model))).Elem()
	} else {
		bind = reflect.New(GetType(model)).Elem()
	}

	// 调用hook函数
	for i := 0; i < len(in2); i++ {
		if res := hook.Call(append(in1, in2[i])); len(res) > 0 {
			var ok bool
			if res[len(res)-1].Type().Implements(errType) {
				if err, ok = res[len(res)-1].Interface().(error); ok && err != nil {
					return
				}
			}

			if !res[0].Type().Implements(errType) {
				if len(batch) > 0 && batch[0] {
					if t := res[0].Type(); t != bind.Type().Elem() {
						bind = reflect.New(reflect.SliceOf(res[0].Type())).Elem()
					}
					bind = reflect.Append(bind, res[0])
				} else {
					if t := res[0].Type(); t != bind.Type() {
						bind = reflect.New(t).Elem()
					}
					bind.Set(res[0])
				}
				continue
			}
		}

		// hook函数没有返回值, 取this值
		if len(batch) > 0 && batch[0] {
			if bind.Type().Elem() != this.Type().Elem() {
				bind = reflect.New(reflect.SliceOf(this.Type().Elem())).Elem()
			}
			bind = reflect.Append(bind, this.Elem())
		} else {
			if bind.Type() != this.Type().Elem() {
				bind = this.Elem()
			} else {
				bind.Set(this.Elem())
			}
		}
	}

	return
}

// CallBindHook bind request params call hook function from model.
func CallBindHook(ctx *gin.Context, this, hook reflect.Value, model interface{}, batch ...bool) (bind reflect.Value, err error) {
	if this.IsValid() && !this.IsNil() && hook.IsValid() && !hook.IsNil() {
		return CallHook(ctx, this, hook, model, batch...)
	}

	if len(batch) > 0 && batch[0] {
		bind = reflect.New(reflect.SliceOf(GetType(model))).Elem()
	} else {
		bind = reflect.New(GetType(model)).Elem()
	}

	if err = ctx.Bind(bind.Addr().Interface()); err != nil {
		return
	}

	return
}

// Get get value from model.
// @Method: GET
// @Params: model element
// @URI: /$model/$field/:value
// @Return: $model
func Get(ctx *gin.Context, db *gorm.DB, model interface{}) (record interface{}, err error) {
	var m = reflect.New(GetType(model)).Elem()

	if err = Where(ctx, db, model, -2).First(m.Addr().Interface()).Error; err != nil {
		if gorm.IsRecordNotFoundError(err) {
			err = nil
		}
		return
	}

	return m.Interface(), nil
}

// List get model list by params.
// @Method: GET
// @Params: page/size/order/omit/select and model element
// @URI: /$model
// @Return: Result
func List(ctx *gin.Context, db *gorm.DB, model interface{}) (result *Result, err error) {
	var scope = db.NewScope(model)
	var query Query
	if err = ctx.Bind(&query); err != nil {
		return
	}

	var total int
	if err = db.Model(model).Count(&total).Error; err != nil {
		return
	}

	db = Where(ctx, db, model)

	var slice = reflect.New(reflect.SliceOf(GetType(model))).Elem()

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

// Create create a model.
// @Method: POST
// @URI: /$model
// @Return: $model
func Create(ctx *gin.Context, db *gorm.DB, model interface{}) (record interface{}, err error) {
	this, hook, _ := BindHook(model, "Create")
	value, err := CallBindHook(ctx, this, hook, model)
	if err != nil {
		return
	}

	if err = db.Create(value.Addr().Interface()).Error; err != nil {
		return
	}

	return value.Interface(), nil
}

// Create create multi models.
// @Method: POST
// @URI: /$model
// @Return: $model[]
func Creates(ctx *gin.Context, db *gorm.DB, model interface{}) (records interface{}, err error) {
	this, hook, _ := BindHook(model, "Create")
	slice, err := CallBindHook(ctx, this, hook, model, true)
	if err != nil {
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

// Update update a model by params.
// @Method: PUT
// @Params: model element
// @URI: /$model/$field/:value
// @Return: affected number
func Update(ctx *gin.Context, db *gorm.DB, model interface{}) (affected int64, err error) {
	this, hook, _ := BindHook(model, "Update")
	value, err := CallBindHook(ctx, this, hook, map[string]interface{}{})
	if err != nil {
		return
	}

	switch v := value.Interface().(type) {
	case map[string]interface{}:
		delete(v, GetField(ctx, -2))
	default:
		if field := value.FieldByName("ID"); field.IsValid() && !field.IsZero() {
			field.Set(reflect.Zero(field.Type()))
		}
	}

	db = Where(ctx, db, model, -2).Update(value.Interface())

	return db.RowsAffected, db.Error
}

// Updates update multi model by field and params.
// @Method: PUT
// @Params: model element
// @URI: /$model/$field
// @Return: affected number
func Updates(ctx *gin.Context, db *gorm.DB, model interface{}) (affected int64, err error) {
	this, hook, _ := BindHook(model, "Update")
	values, err := CallBindHook(ctx, this, hook, map[string]interface{}{}, true)
	if err != nil {
		return
	}

	if values.Len() == 0 {
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

	field := GetField(ctx, -1)
	tx = Where(ctx, tx, model)

	for i := 0; i < values.Len(); i++ {
		value := values.Index(i)
		var v interface{}
		if f, exists := db.NewScope(value.Interface()).FieldByName(field); exists {
			v = f.Field.Interface()
		}
		if m, ok := value.Interface().(map[string]interface{}); ok {
			v = m[field]
			delete(m, field)
		}
		tx := tx.Where(field+" = ?", v).Updates(value.Interface())
		if err = tx.Error; err != nil {
			return
		}
		affected += tx.RowsAffected
	}

	return
}

// Delete delete a model by field and params.
// @Method: DELETE
// @Params: model element
// @URI: /$model/$field/:value
// @Return: affected number
func Delete(ctx *gin.Context, db *gorm.DB, model interface{}) (affected int64, err error) {
	db = Where(ctx, db, model, -2).Delete(model)

	return db.RowsAffected, db.Error
}

// Deletes delete multi model by field and params.
// @Method: DELETE
// @Params: model element
// @URI: /$model/$field
// @Return: affected number
func Deletes(ctx *gin.Context, db *gorm.DB, model interface{}) (affected int64, err error) {
	var values []interface{}
	if err = ctx.Bind(&values); err != nil {
		return
	}

	if len(values) == 0 {
		return
	}

	field := GetField(ctx, -1)

	db = Where(ctx, db, model).Where(field+" IN (?)", values).Delete(model)

	return db.RowsAffected, db.Error
}

// Mount mount model to db and restful api.
func Mount(mux gin.IRouter, db *gorm.DB, model interface{}, handler ...Handler) gin.IRouter {
	var h Handler = Default
	if len(handler) > 0 {
		h = handler[0]
	}

	get := func(ctx *gin.Context) {
		record, err := Get(ctx, useDB(ctx, db), model)
		h.Return(ctx, record, err)
	}

	list := func(ctx *gin.Context) {
		result, err := List(ctx, useDB(ctx, db), model)
		h.Return(ctx, result, err)
	}

	create := func(ctx *gin.Context) {
		record, err := Create(ctx, useDB(ctx, db), model)
		h.Return(ctx, record, err)
	}

	creates := func(ctx *gin.Context) {
		records, err := Creates(ctx, useDB(ctx, db), model)
		h.Return(ctx, records, err)
	}

	update := func(ctx *gin.Context) {
		affected, err := Update(ctx, useDB(ctx, db), model)
		h.Return(ctx, affected, err)
	}

	updates := func(ctx *gin.Context) {
		affected, err := Updates(ctx, useDB(ctx, db), model)
		h.Return(ctx, affected, err)
	}

	del := func(ctx *gin.Context) {
		affected, err := Delete(ctx, useDB(ctx, db), model)
		h.Return(ctx, affected, err)
	}

	deletes := func(ctx *gin.Context) {
		affected, err := Deletes(ctx, useDB(ctx, db), model)
		h.Return(ctx, affected, err)
	}

	// generate restful api
	var scope = db.NewScope(model)
	for _, f := range scope.Fields() {
		for _, name := range []string{f.Name, f.DBName} {
			field := mux.Group(name)
			{
				field.PUT("", updates)
				field.DELETE("", deletes)
			}

			value := field.Group("/:value")
			{
				value.GET("", get)
				value.PUT("", update)
				value.DELETE("", del)
			}

			if f.Name == f.DBName {
				break
			}
		}
	}

	// generate batch restful api
	mux.GET("", list)
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
			creates(ctx)
			return
		}

		create(ctx)
	})

	return mux
}
