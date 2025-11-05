package xconn

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/util"
)

type Value struct {
	data any
}

func NewValue(v any) Value {
	return Value{data: v}
}

func (v Value) Raw() any {
	return v.data
}

func (v Value) String() (string, error) {
	if s, ok := v.data.(string); ok {
		return s, nil
	}
	return "", fmt.Errorf("value is not data string, got %T", v.data)
}

func (v Value) StringOr(def string) string {
	if s, err := v.String(); err == nil {
		return s
	}
	return def
}

func (v Value) Bool() (bool, error) {
	if b, ok := v.data.(bool); ok {
		return b, nil
	}
	return false, fmt.Errorf("value is not data bool, got %T", v.data)
}

func (v Value) BoolOr(def bool) bool {
	if b, err := v.Bool(); err == nil {
		return b
	}
	return def
}

func (v Value) Int64() (int64, error) {
	switch val := v.data.(type) {
	case int64:
		return val, nil
	case uint64:
		if val > math.MaxInt64 {
			return 0, fmt.Errorf("uint64 value %d overflows int64", val)
		}
		return int64(val), nil
	case uint8:
		return int64(val), nil
	case int:
		return int64(val), nil
	case int8:
		return int64(val), nil
	case int32:
		return int64(val), nil
	case uint:
		if val > math.MaxInt64 {
			return 0, fmt.Errorf("uint value %d overflows int64", val)
		}
		return int64(val), nil
	case uint16:
		return int64(val), nil
	case uint32:
		return int64(val), nil
	case float64:
		if val > math.MaxInt64 || val < math.MinInt64 {
			return 0, fmt.Errorf("float64 value %f overflows int64", val)
		}
		return int64(val), nil
	case float32:
		if val > math.MaxInt64 || val < math.MinInt64 {
			return 0, fmt.Errorf("float32 value %f overflows int64", val)
		}
		return int64(val), nil
	}
	return 0, fmt.Errorf("value cannot be converted to int64, got %T", v.data)
}

func (v Value) Int64Or(def int64) int64 {
	if i, err := v.Int64(); err == nil {
		return i
	}
	return def
}

func (v Value) Float64() (float64, error) {
	switch val := v.data.(type) {
	case float64:
		return val, nil
	case float32:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case int:
		return float64(val), nil
	}
	return 0, fmt.Errorf("value cannot be converted to float64, got %T", v.data)
}

func (v Value) Float64Or(def float64) float64 {
	if f, err := v.Float64(); err == nil {
		return f
	}
	return def
}

func (v Value) UInt64() (uint64, error) {
	switch val := v.data.(type) {
	case uint64:
		return val, nil
	case uint:
		return uint64(val), nil
	case uint8:
		return uint64(val), nil
	case uint16:
		return uint64(val), nil
	case uint32:
		return uint64(val), nil
	case int64:
		if val >= 0 {
			return uint64(val), nil
		}
	case int:
		if val >= 0 {
			return uint64(val), nil
		}
	case float64:
		if val >= 0 {
			return uint64(val), nil
		}
	}
	return 0, fmt.Errorf("value cannot be converted to uint64, got %T", v.data)
}

func (v Value) UInt64Or(def uint64) uint64 {
	if u, err := v.UInt64(); err == nil {
		return u
	}
	return def
}

func (v Value) Bytes() ([]byte, error) {
	if b, ok := v.data.([]byte); ok {
		return b, nil
	}
	if s, ok := v.data.(string); ok {
		return []byte(s), nil
	}
	return nil, fmt.Errorf("value cannot be converted to []byte, got %T", v.data)
}

func (v Value) BytesOr(def []byte) []byte {
	if b, err := v.Bytes(); err == nil {
		return b
	}
	return def
}

func (v Value) Decode(out any) error {
	data, err := json.Marshal(v.data)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	if err = json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("failed to decode value: %w", err)
	}

	return nil
}

func (v Value) List() (List, error) {
	items, ok := v.data.([]any)
	if !ok {
		return nil, fmt.Errorf("value is not []any, got %T", v.data)
	}

	list := make(List, len(items))
	for i, item := range items {
		list[i] = NewValue(item)
	}

	return list, nil
}

func (v Value) ListOr(def List) List {
	if b, err := v.List(); err == nil {
		return b
	}
	return def
}

func (v Value) Dict() (Dict, error) {
	items, ok := v.data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("value is not map[string]any, got %T", v.data)
	}

	dict := make(Dict, len(items))
	for k, item := range items {
		dict[k] = NewValue(item)
	}

	return dict, nil
}

func (v Value) DictOr(def Dict) Dict {
	if b, err := v.Dict(); err == nil {
		return b
	}
	return def
}

type List []Value

func NewList(values []any) List {
	list := make(List, len(values))
	for i, v := range values {
		list[i] = NewValue(v)
	}
	return list
}

func (l List) Len() int {
	return len(l)
}

func (l List) Get(i int) (Value, error) {
	if i < 0 || i >= len(l) {
		return Value{}, fmt.Errorf("index %d out of range [0, %d]", i, len(l))
	}
	return l[i], nil
}

func (l List) GetOr(i int, def any) Value {
	if v, err := l.Get(i); err == nil {
		return v
	}
	return NewValue(def)
}

func (l List) String(i int) (string, error) {
	v, err := l.Get(i)
	if err != nil {
		return "", err
	}
	return v.String()
}

func (l List) StringOr(i int, def string) string {
	return l.GetOr(i, def).StringOr(def)
}

func (l List) Bool(i int) (bool, error) {
	v, err := l.Get(i)
	if err != nil {
		return false, err
	}
	return v.Bool()
}

func (l List) BoolOr(i int, def bool) bool {
	return l.GetOr(i, def).BoolOr(def)
}

func (l List) Int64(i int) (int64, error) {
	v, err := l.Get(i)
	if err != nil {
		return 0, err
	}
	return v.Int64()
}

func (l List) Int64Or(i int, def int64) int64 {
	return l.GetOr(i, def).Int64Or(def)
}

func (l List) Float64(i int) (float64, error) {
	v, err := l.Get(i)
	if err != nil {
		return 0, err
	}
	return v.Float64()
}

func (l List) Float64Or(i int, def float64) float64 {
	return l.GetOr(i, def).Float64Or(def)
}

func (l List) UInt64(i int) (uint64, error) {
	v, err := l.Get(i)
	if err != nil {
		return 0, err
	}
	return v.UInt64()
}

func (l List) UInt64Or(i int, def uint64) uint64 {
	return l.GetOr(i, def).UInt64Or(def)
}

func (l List) Bytes(i int) ([]byte, error) {
	v, err := l.Get(i)
	if err != nil {
		return nil, err
	}
	return v.Bytes()
}

func (l List) BytesOr(i int, def []byte) []byte {
	return l.GetOr(i, def).BytesOr(def)
}

func (l List) Decode(out any) error {
	raw := make(map[int]any, len(l))
	for i, v := range l {
		raw[i] = v.Raw()
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("failed to marshal args: %w", err)
	}

	if err = json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("failed to decode args into struct: %w", err)
	}

	return nil
}

func (l List) List(i int) (List, error) {
	v, err := l.Get(i)
	if err != nil {
		return nil, err
	}
	return v.List()
}

func (l List) ListOr(i int, def List) List {
	return l.GetOr(i, def).ListOr(def)
}

func (l List) Dict(i int) (Dict, error) {
	v, err := l.Get(i)
	if err != nil {
		return nil, err
	}
	return v.Dict()
}

func (l List) DictOr(i int, def Dict) Dict {
	return l.GetOr(i, def).DictOr(def)
}

func (l List) Raw() []any {
	raw := make([]any, len(l))
	for i, v := range l {
		raw[i] = v.Raw()
	}
	return raw
}

type Dict map[string]Value

func NewDict(values map[string]any) Dict {
	dict := make(Dict, len(values))
	for k, v := range values {
		dict[k] = NewValue(v)
	}
	return dict
}

func (d Dict) Len() int {
	return len(d)
}

func (d Dict) Get(key string) (Value, error) {
	if v, ok := d[key]; ok {
		return v, nil
	}
	return Value{}, fmt.Errorf("key %q not found", key)
}

func (d Dict) GetOr(key string, def any) Value {
	if v, err := d.Get(key); err == nil {
		return v
	}
	return NewValue(def)
}

func (d Dict) Has(key string) bool {
	_, ok := d[key]
	return ok
}

func (d Dict) String(key string) (string, error) {
	v, err := d.Get(key)
	if err != nil {
		return "", err
	}
	return v.String()
}

func (d Dict) StringOr(key string, def string) string {
	return d.GetOr(key, def).StringOr(def)
}

func (d Dict) Bool(key string) (bool, error) {
	v, err := d.Get(key)
	if err != nil {
		return false, err
	}
	return v.Bool()
}

func (d Dict) BoolOr(key string, def bool) bool {
	return d.GetOr(key, def).BoolOr(def)
}

func (d Dict) Int64(key string) (int64, error) {
	v, err := d.Get(key)
	if err != nil {
		return 0, err
	}
	return v.Int64()
}

func (d Dict) Int64Or(key string, def int64) int64 {
	return d.GetOr(key, def).Int64Or(def)
}

func (d Dict) Float64(key string) (float64, error) {
	v, err := d.Get(key)
	if err != nil {
		return 0, err
	}
	return v.Float64()
}

func (d Dict) Float64Or(key string, def float64) float64 {
	return d.GetOr(key, def).Float64Or(def)
}

func (d Dict) UInt64(key string) (uint64, error) {
	v, err := d.Get(key)
	if err != nil {
		return 0, err
	}
	return v.UInt64()
}

func (d Dict) UInt64Or(key string, def uint64) uint64 {
	return d.GetOr(key, def).UInt64Or(def)
}

func (d Dict) Bytes(key string) ([]byte, error) {
	v, err := d.Get(key)
	if err != nil {
		return nil, err
	}
	return v.Bytes()
}

func (d Dict) BytesOr(key string, def []byte) []byte {
	return d.GetOr(key, def).BytesOr(def)
}

func (d Dict) Decode(out any) error {
	raw := make(map[string]any, len(d))
	for key, v := range d {
		raw[key] = v.Raw()
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("failed to marshal kwargs: %w", err)
	}

	if err = json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("failed to decode kwargs into struct: %w", err)
	}

	return nil
}

func (d Dict) List(key string) (List, error) {
	v, err := d.Get(key)
	if err != nil {
		return nil, err
	}
	return v.List()
}

func (d Dict) ListOr(key string, def List) List {
	return d.GetOr(key, def).ListOr(def)
}

func (d Dict) Dict(key string) (Dict, error) {
	v, err := d.Get(key)
	if err != nil {
		return nil, err
	}
	return v.Dict()
}

func (d Dict) DictOr(key string, def Dict) Dict {
	return d.GetOr(key, def).DictOr(def)
}

func (d Dict) Raw() map[string]any {
	raw := make(map[string]any, len(d))
	for key, v := range d {
		raw[key] = v.Raw()
	}
	return raw
}

type Invocation struct {
	args    List
	kwargs  Dict
	details Dict

	SendProgress SendProgress
}

func NewInvocation(args []any, kwargs map[string]any, details map[string]any) *Invocation {
	return &Invocation{
		args:    NewList(args),
		kwargs:  NewDict(kwargs),
		details: NewDict(details),
	}
}

func (inv *Invocation) ArgUInt64(index int) (uint64, error) {
	return inv.args.UInt64(index)
}

func (inv *Invocation) ArgUInt64Or(index int, def uint64) uint64 {
	return inv.args.UInt64Or(index, def)
}

func (inv *Invocation) ArgString(index int) (string, error) {
	return inv.args.String(index)
}

func (inv *Invocation) ArgStringOr(index int, def string) string {
	return inv.args.StringOr(index, def)
}

func (inv *Invocation) ArgBool(index int) (bool, error) {
	return inv.args.Bool(index)
}

func (inv *Invocation) ArgBoolOr(index int, def bool) bool {
	return inv.args.BoolOr(index, def)
}

func (inv *Invocation) ArgFloat64(index int) (float64, error) {
	return inv.args.Float64(index)
}

func (inv *Invocation) ArgFloat64Or(index int, def float64) float64 {
	return inv.args.Float64Or(index, def)
}

func (inv *Invocation) ArgInt64(index int) (int64, error) {
	return inv.args.Int64(index)
}

func (inv *Invocation) ArgInt64Or(index int, def int64) int64 {
	return inv.args.Int64Or(index, def)
}

func (inv *Invocation) ArgBytes(index int) ([]byte, error) {
	return inv.args.Bytes(index)
}

func (inv *Invocation) ArgsStruct(out any) error {
	return inv.args.Decode(out)
}

func (inv *Invocation) ArgBytesOr(index int, def []byte) []byte {
	return inv.args.BytesOr(index, def)
}

func (inv *Invocation) ArgsLen() int {
	return inv.args.Len()
}

func (inv *Invocation) ArgList(index int) (List, error) {
	return inv.args.List(index)
}

func (inv *Invocation) ArgListOr(index int, def List) List {
	return inv.args.ListOr(index, def)
}

func (inv *Invocation) ArgDict(index int) (Dict, error) {
	return inv.args.Dict(index)
}

func (inv *Invocation) ArgDictOr(index int, def Dict) Dict {
	return inv.args.DictOr(index, def)
}

func (inv *Invocation) Args() []any {
	return inv.args.Raw()
}

func (inv *Invocation) KwargUInt64(key string) (uint64, error) {
	return inv.kwargs.UInt64(key)
}

func (inv *Invocation) KwargUInt64Or(key string, def uint64) uint64 {
	return inv.kwargs.UInt64Or(key, def)
}

func (inv *Invocation) KwargString(key string) (string, error) {
	return inv.kwargs.String(key)
}

func (inv *Invocation) KwargStringOr(key string, def string) string {
	return inv.kwargs.StringOr(key, def)
}

func (inv *Invocation) KwargBool(key string) (bool, error) {
	return inv.kwargs.Bool(key)
}

func (inv *Invocation) KwargBoolOr(key string, def bool) bool {
	return inv.kwargs.BoolOr(key, def)
}

func (inv *Invocation) KwargFloat64(key string) (float64, error) {
	return inv.kwargs.Float64(key)
}

func (inv *Invocation) KwargFloat64Or(key string, def float64) float64 {
	return inv.kwargs.Float64Or(key, def)
}

func (inv *Invocation) KwargInt64(key string) (int64, error) {
	return inv.kwargs.Int64(key)
}

func (inv *Invocation) KwargInt64Or(key string, def int64) int64 {
	return inv.kwargs.Int64Or(key, def)
}

func (inv *Invocation) KwargBytes(key string) ([]byte, error) {
	return inv.kwargs.Bytes(key)
}

func (inv *Invocation) KwargBytesOr(key string, def []byte) []byte {
	return inv.kwargs.BytesOr(key, def)
}

func (inv *Invocation) KwargList(key string) (List, error) {
	return inv.kwargs.List(key)
}

func (inv *Invocation) KwargListOr(key string, def List) List {
	return inv.kwargs.ListOr(key, def)
}

func (inv *Invocation) KwargDict(key string) (Dict, error) {
	return inv.kwargs.Dict(key)
}

func (inv *Invocation) KwargDictOr(key string, def Dict) Dict {
	return inv.kwargs.DictOr(key, def)
}

func (inv *Invocation) KwargsStruct(out any) error {
	return inv.kwargs.Decode(out)
}

func (inv *Invocation) KwargsLen() int {
	return inv.kwargs.Len()
}

func (inv *Invocation) Kwargs() map[string]any {
	return inv.kwargs.Raw()
}

func (inv *Invocation) Details() map[string]any {
	return inv.details.Raw()
}

func (inv *Invocation) Procedure() string {
	return inv.details.StringOr("procedure", "")
}

func (inv *Invocation) Caller() uint64 {
	return inv.details.UInt64Or("caller", 0)
}

func (inv *Invocation) CallerAuthID() string {
	return inv.details.StringOr("caller_authid", "")
}

func (inv *Invocation) CallerAuthRole() string {
	return inv.details.StringOr("caller_authrole", "")
}

func (inv *Invocation) Progress() bool {
	return util.ToBool(inv.Details()[wampproto.OptionProgress])
}

type Event struct {
	args    List
	kwargs  Dict
	details Dict
}

func NewEvent(args []any, kwargs map[string]any, details map[string]any) *Event {
	return &Event{
		args:    NewList(args),
		kwargs:  NewDict(kwargs),
		details: NewDict(details),
	}
}

func (e *Event) ArgUInt64(index int) (uint64, error) {
	return e.args.UInt64(index)
}

func (e *Event) ArgUInt64Or(index int, def uint64) uint64 {
	return e.args.UInt64Or(index, def)
}

func (e *Event) ArgString(index int) (string, error) {
	return e.args.String(index)
}

func (e *Event) ArgStringOr(index int, def string) string {
	return e.args.StringOr(index, def)
}

func (e *Event) ArgBool(index int) (bool, error) {
	return e.args.Bool(index)
}

func (e *Event) ArgBoolOr(index int, def bool) bool {
	return e.args.BoolOr(index, def)
}

func (e *Event) ArgFloat64(index int) (float64, error) {
	return e.args.Float64(index)
}

func (e *Event) ArgFloat64Or(index int, def float64) float64 {
	return e.args.Float64Or(index, def)
}

func (e *Event) ArgInt64(index int) (int64, error) {
	return e.args.Int64(index)
}

func (e *Event) ArgInt64Or(index int, def int64) int64 {
	return e.args.Int64Or(index, def)
}

func (e *Event) ArgBytes(index int) ([]byte, error) {
	return e.args.Bytes(index)
}

func (e *Event) ArgsStruct(out any) error {
	return e.args.Decode(out)
}

func (e *Event) ArgBytesOr(index int, def []byte) []byte {
	return e.args.BytesOr(index, def)
}

func (e *Event) ArgList(index int) (list List, err error) {
	return e.args.List(index)
}

func (e *Event) ArgListOr(index int, def List) List {
	return e.args.ListOr(index, def)
}

func (e *Event) ArgDict(index int) (dict Dict, err error) {
	return e.args.Dict(index)
}

func (e *Event) ArgDictOr(index int, def Dict) Dict {
	return e.args.DictOr(index, def)
}

func (e *Event) ArgsLen() int {
	return e.args.Len()
}

func (e *Event) Args() []any {
	return e.args.Raw()
}

func (e *Event) KwargUInt64(key string) (uint64, error) {
	return e.kwargs.UInt64(key)
}

func (e *Event) KwargUInt64Or(key string, def uint64) uint64 {
	return e.kwargs.UInt64Or(key, def)
}

func (e *Event) KwargString(key string) (string, error) {
	return e.kwargs.String(key)
}

func (e *Event) KwargStringOr(key string, def string) string {
	return e.kwargs.StringOr(key, def)
}

func (e *Event) KwargBool(key string) (bool, error) {
	return e.kwargs.Bool(key)
}

func (e *Event) KwargBoolOr(key string, def bool) bool {
	return e.kwargs.BoolOr(key, def)
}

func (e *Event) KwargFloat64(key string) (float64, error) {
	return e.kwargs.Float64(key)
}

func (e *Event) KwargFloat64Or(key string, def float64) float64 {
	return e.kwargs.Float64Or(key, def)
}

func (e *Event) KwargInt64(key string) (int64, error) {
	return e.kwargs.Int64(key)
}

func (e *Event) KwargInt64Or(key string, def int64) int64 {
	return e.kwargs.Int64Or(key, def)
}

func (e *Event) KwargBytes(key string) ([]byte, error) {
	return e.kwargs.Bytes(key)
}

func (e *Event) KwargBytesOr(key string, def []byte) []byte {
	return e.kwargs.BytesOr(key, def)
}

func (e *Event) KwargList(key string) (List, error) {
	return e.kwargs.List(key)
}

func (e *Event) KwargListOr(key string, def List) List {
	return e.kwargs.ListOr(key, def)
}

func (e *Event) KwargDict(key string) (Dict, error) {
	return e.kwargs.Dict(key)
}

func (e *Event) KwargDictOr(key string, def Dict) Dict {
	return e.kwargs.DictOr(key, def)
}

func (e *Event) KwargsStruct(out any) error {
	return e.kwargs.Decode(out)
}

func (e *Event) KwargsLen() int {
	return e.kwargs.Len()
}

func (e *Event) Kwargs() map[string]any {
	return e.kwargs.Raw()
}

func (e *Event) Details() map[string]any {
	return e.details.Raw()
}

func (e *Event) Topic() string {
	return e.details.StringOr("topic", "")
}

func (e *Event) Publisher() uint64 {
	return e.details.UInt64Or("publisher", 0)
}

func (e *Event) PublisherAuthID() string {
	return e.details.StringOr("publisher_authid", "")
}

func (e *Event) PublisherAuthRole() string {
	return e.details.StringOr("publisher_authrole", "")
}
