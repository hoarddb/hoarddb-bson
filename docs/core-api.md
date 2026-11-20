# The new core's method set, as built

Spec section 5.2 fixes the constraints of the new core and leaves the exact
method set to the implementation. This file records it as each step landed. A change to a name or a signature here is a reviewed change.

## Step 2a: `Document`, `Limits`, `Validate`, `TrustStored`

Package `github.com/hoarddb/hoarddb-bson/bson`:

```go
type Document struct{ /* b []byte, unexported */ }
func (document Document) Bytes() []byte

func Validate(raw []byte, limits Limits) (Document, error)
func TrustStored(raw []byte) (Document, error)

type Limits struct {
	Depth int // 1 to MaxNestingCeiling; no default
	Size  int // 0 means no size check
}
const MaxNestingCeiling = 200

const TypeBinaryGeneric = bsontype.BinaryGeneric
const TypeBinaryFunction = bsontype.BinaryFunction
const TypeBinaryBinaryOld = bsontype.BinaryBinaryOld
const TypeBinaryUUIDOld = bsontype.BinaryUUIDOld
const TypeBinaryMD5 = bsontype.BinaryMD5
const TypeBinaryEncrypted = bsontype.BinaryEncrypted
const TypeBinaryColumn = bsontype.BinaryColumn
const TypeBinarySensitive = bsontype.BinarySensitive
const TypeBinaryUserDefined = bsontype.BinaryUserDefined

var ErrMalformedDocument error
var ErrNestingTooDeep error
var ErrDocumentTooLarge error
var ErrInvalidLimits error

type ValidationError struct {
	Err    error  // one of ErrMalformedDocument, ErrNestingTooDeep, ErrDocumentTooLarge
	Offset int    // the byte at which the check stopped
	Reason string // what was wrong there
}
func (validationError *ValidationError) Error() string
func (validationError *ValidationError) Unwrap() error
```

Package `github.com/hoarddb/hoarddb-bson/bson/bsontype` mirrors the vendor's
`bson/bsontype` exactly: the 21 element-type constants, the 10 binary-subtype
constants, `type Type byte`, `Type.String` and `Type.IsValid`. It was pulled
forward from step 2c into 2a by the coordinator, because `Validate` and the
2b cursor switch on element types.

### Decisions the spec left open

- **`Document` is a struct with one unexported field.** `bson.Document(b)` and
  `[]byte(d)` are compile errors (`TestDocumentIsAnOpaqueStruct` type-checks
  both). `Bytes` returns the bytes uncopied. `Raw()` arrives with `bson.Raw` in
  step 4a (SE-7). The builder's `Document()` arrives in step 2c.
- **The zero `Document`** (`var document bson.Document`) is the one value no
  constructor made. It holds no bytes and is not a validated document, not
  even the empty one. `Validate` and `TrustStored` return it with every
  error, and never with a nil error. Each method's handling:
  - `Bytes` returns nil and reads nothing, so it is the one method that
    takes the zero value without an error.
  - Every later method that reads the bytes refuses the zero value with an
    error and never panics on it: `Lookup` (2b), `Raw` (4a) and `Decode`
    (4f). Each step adds a test for its method, beside
    `TestZeroDocumentHoldsNoBytes`.
- **`Limits.Depth` has no default.** Zero, a negative depth, or a depth above
  `MaxNestingCeiling` returns an error matching `ErrInvalidLimits`. This
  means a call site that leaves out its depth fails the first time it runs,
  instead of quietly getting a uniform limit (build doc trap 5).
  `Limits.Size` zero means no size check, as the payload boundaries need
  (spec 5.2, call C4). A negative size is invalid.
- **Depth counting** matches the server's scan: the root is level 1, and an
  embedded document, an array or a code-with-scope's scope each opens one
  more level. A level past `Depth` is refused when it opens, before anything
  below it is read.
- **One sentinel for depth.** `ErrNestingTooDeep` is both `Validate`'s depth
  refusal and, from step 4f, the ceiling refusal of the mirrored `[]byte`
  entry points (spec 5.2).
- **What `Validate` accepts** is what the server's depth scan at `Depth`
  and the vendor's decode into `bson.D` both accept (spec 6.2, call C3).
  It is one pass with no copy. It tracks up to 128 levels with no allocation
  and allocates its stack only when a document goes deeper. That includes
  the scan's old-binary rule. The vendor's decoder never reads a subtype 0x02
  binary's outer length. The scan holds that length to the bytes and to the
  inner length plus 4. So an old binary whose outer length runs past the
  bytes, or disagrees with the inner one, is refused, although the vendor's
  decode alone would read it. `FuzzValidateDifferential` found the first
  case. `Validate` does not check UTF-8 (O10). It does not check
  the last byte of a code-with-scope's code string, because the vendor's
  decode does not check it either.
- **Refusal order.** A document both too deep and malformed further on is
  refused for whichever problem the single pass meets first. The server's
  scan, followed by a decode, would report the depth first whenever the deep
  level comes before a malformed value byte. Only whether the input is
  accepted is part of the oracle. Which sentinel a doubly-bad input carries
  is not.
- **`TrustStored`** checks only the framing, in O(1). It needs at least 5
  bytes, a length prefix equal to `len(raw)`, and a last byte of 0. The
  5-byte minimum is a reading of the spec's rule: without it, the 4 bytes
  `04 00 00 00` would pass ("prefix equals the length, last byte 0") although
  they are not a document. Who may call it is confined in hoardDB by the
  step 7b allowlist test. The library cannot enforce that rule.
- **Errors** are a `*ValidationError` carrying the sentinel, the byte
  offset and a reason. The success path allocates nothing, because every
  reason is a constant until an error is built.

## Step 2c: `Builder`

Package `github.com/hoarddb/hoarddb-bson/bson`:

```go
type Builder struct{ /* buffer, open levels, first error; unexported */ }
func (builder *Builder) AppendDouble(key string, number float64)
func (builder *Builder) AppendString(key, text string)
func (builder *Builder) AppendDocument(key string, document Document)
func (builder *Builder) AppendBinary(key string, subtype byte, payload []byte)
func (builder *Builder) AppendObjectID(key string, id primitive.ObjectID)
func (builder *Builder) AppendBoolean(key string, flag bool)
func (builder *Builder) AppendDateTime(key string, milliseconds int64)
func (builder *Builder) AppendNull(key string)
func (builder *Builder) AppendInt32(key string, number int32)
func (builder *Builder) AppendInt64(key string, number int64)
func (builder *Builder) StartDocument(key string)
func (builder *Builder) StartArray(key string)
func (builder *Builder) End()
func (builder *Builder) Document() (Document, error)

var ErrInvalidKey error        // a key with a zero byte, or a key other than "" inside an array
var ErrUnbalancedBuilder error // End with no level open, or Document with one still open
```

### Decisions the spec left open

- **`Builder.Document()` is the builder constructor of spec 5.2.** It wraps
  the bytes with no validation pass: every method writes well-framed
  bytes. Its output equals the vendor's `bsoncore.BuildDocument` of the
  same `Append*Element` values (`TestBuilderMatchesBuildDocument`).
- **The zero `Builder` is ready to use.** The root opens on the first call.
- **Errors, not panics.** The vendor's `bsoncore.AppendHeader` panics on a
  key with a zero byte. `Builder` is the form that returns an error instead:
  `ErrInvalidKey`. Unbalanced levels are `ErrUnbalancedBuilder`, a zero
  `Document` passed to `AppendDocument` is `ErrMalformedDocument`, and a
  document or array longer than an int32 length is `ErrDocumentTooLarge`.
  The first error is kept, later calls do nothing, and `Document` returns
  it.
- **Array keys are the builder's.** Inside `StartArray`, every key is `""`
  and the builder writes `"0"`, `"1"`, ... Any other key is
  `ErrInvalidKey`, so an array cannot be built with keys out of order.
- **`Document` resets the builder onto a new buffer.** A returned
  `Document`'s bytes are never written again, so the builder cannot break
  `Document`'s immutability. The cost is one buffer per document.
- **`AppendDocument` copies the appended document's bytes and does not
  walk them.** Its depth is not added to anything: a `Document` from
  `TrustStored` was never measured. The builder places no depth limit of
  its own. `StartDocument` and `StartArray` nest without a ceiling, the way
  the vendor's functions do. A reader that needs one applies `Validate`.
- **The element set** is the scalar types the tree builds with bsoncore
  today, plus booleans and null. Decimal128, Timestamp, regex, the
  JavaScript types and the legacy types are not in it. A later step adds
  any it needs.

## Step 2c: mirrored `bsoncore`

Package `github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore` holds the
inventoried `Append*`, `Build*` and `Read*` functions with the vendor's
signatures (`TestFunctionSignaturesMatchVendor`), and `Value` with its two
fields only. `Value`'s methods, `Document`, `Element`, `ReadDocument` and
`ReadElement` are step 4b's (build doc SE-7). `AppendHeader` keeps the
vendor's panic on a key with a zero byte (the only `panic` call in the
package), and `AppendDocumentEnd` and `AppendArrayEnd` keep its runtime
panic on a negative index.

## Step 2b: `Cursor`, `View`, `Path`, `Segment`, `Document.Lookup`

Package `github.com/hoarddb/hoarddb-bson/bson`:

```go
type View struct{ /* type and value bytes, unexported */ }
func (view View) Type() bsontype.Type
func (view View) Bytes() []byte

type Cursor struct{ /* unexported */ }
func (document Document) Cursor() Cursor
func (cursor *Cursor) Next() bool
func (cursor *Cursor) Key() []byte
func (cursor *Cursor) KeyEquals(name string) bool
func (cursor *Cursor) Value() View
func (cursor *Cursor) Err() error

type Segment struct{ /* unexported */ }
func Name(name string) Segment
func Position(position int) Segment
type Path []Segment
func (document Document) Lookup(path Path) (View, bool, error)

var ErrZeroDocument error
```

### Decisions the spec left open

- **The view is `View`**, a struct with unexported fields, read through
  `Type` and `Bytes`. `Bytes` is the value as it sits in the document,
  uncopied: a string with its length prefix and terminator, an embedded
  document or an array whole. That is what `bsoncore.Value.Data` and
  `bson.RawValue.Value` hold for the same value, and the differential
  compares them. A miss returns the zero `View` (type 0, nil bytes).
- **`Lookup` returns `(View, bool, error)`.** `found` is
  `server/fieldpath.go:resolveRawPath`'s answer. A missing name, a step
  through a scalar, a name on an array, a position on a document, a position
  past the end or negative, and malformed bytes are all "not found" with a
  nil error. The error is the zero-Document refusal and nothing else.
- **The zero `Document` is refused** (2a's rule, coordinator's note): `Lookup`
  returns not found and an error matching the new `ErrZeroDocument`. A
  cursor of the zero Document has no elements, and its `Err` matches
  `ErrZeroDocument`. The empty document from a constructor is not refused.
  `TestZeroDocumentIsRefusedByTheCursorAndLookup` pins both.
- **The empty path finds nothing**, as the vendor's `LookupErr()` with no
  keys refuses (`ErrEmptyKey`). The server never builds one.
- **`Name("")` is a name.** It finds an element whose key is empty, as the
  vendor does. The server's path grammar refuses empty names before it
  resolves. That is the caller's rule, not the resolver's.
- **A code-with-scope's scope is not a document to a path.** A name after a
  code-with-scope is absent, as with the vendor (`InvalidDepthTraversalError`)
  and the server.
- **The cursor is an iterator** (`Next`, then `Key` and `Value`), a value
  kept on the stack. It skips each value by its length and does not look
  inside it. Its length rules: a string, JavaScript or symbol length is at
  least 1, an embedded document or array at least 5, a code-with-scope at
  least 14, a binary or DBPointer length is not negative, and every value
  lies within its document's elements, never over the terminator. On a
  `Validate`d document none of these can fail.
- **Malformed bytes end the walk quietly.** A `TrustStored` document is
  framed and not walked, so its inside may be malformed. The cursor stops
  there, and `Err` returns a `*ValidationError` matching
  `ErrMalformedDocument` at the offset of the element it could not read.
  `Lookup` treats the rest as absent, as `resolveRawPath`'s walk does. The
  cursor's length rules are stricter than the vendor's `ReadElement`
  (which, for example, accepts a negative string length). The difference
  can show only inside a malformed stored document.

## Step 3c: `Compare`, `Literal`, `CompareLiteral`

Package `github.com/hoarddb/hoarddb-bson/bson`:

```go
func Compare(left, right View) int

type Literal struct{ /* sort key, unexported */ }
func Int32Literal(number int32) Literal
func Int64Literal(number int64) Literal
func DoubleLiteral(number float64) Literal
func StringLiteral(text string) Literal
func DateTimeLiteral(milliseconds int64) Literal
func BooleanLiteral(flag bool) Literal
func NullLiteral() Literal
func CompareLiteral(view View, literal Literal) int
```

### The order

The order is the unindexed sort's (spec 6.3, ruling O9). Each rule's source:

| Rule | Source |
| --- | --- |
| Class first: number 1, string 2, date 3, boolean 4, document 5, array 6, other 7, missing or null 8 | spec 6.3 |
| Number = int32, int64, double | spec 5.2 |
| Numbers by exact value: integers past 2^53 never rounded | spec 6.3 ("exact integer comparison above 2^53") |
| NaN below every number, tied with every NaN | spec 6.3 |
| -0 ties with 0 | build doc step 3c's "±0" row; value equality |
| Strings byte by byte, no collation, no UTF-8 decoding | build doc step 3c ("string byte order") |
| Dates by milliseconds | spec 6.3 class "date", ordered by value |
| `false` before `true` | the HQL reference's sort-order list |
| Documents tie, arrays tie | spec 6.3 |
| Other: by type byte, tied within a type | spec 6.3 ("sub-ranked by BSON type byte", "each rank-7 type tied") |
| Decimal128 is other (0x13), all tied | ruling O9 |
| Timestamp is other (0x11) | the HQL reference's list ("a timestamp" under every other type) |
| Missing (the zero View) ties with null | spec 6.3 ("missing or null 8") |
| Symbol (0x0E), JavaScript (0x0D) and undefined (0x06) are other | read from the type names, not stated: see the step 3c report |

### Decisions the spec left open

- **Two forms.** `Compare` takes two Views; `CompareLiteral` takes a View
  and a `Literal`, a value written in Go, and orders exactly as `Compare`
  against a View holding that value (`TestCompareLiteralMatchesCompare`).
- **Literals cover the classes compared by value**: the three number
  types, string, date, boolean, and null. A document, an array or an
  other-class value ties with every value of its class or type, so a
  literal of one would carry only its type; none is offered until a call
  site needs one.
- **The zero `Literal` is null**, as the zero View is missing, and they tie.
- **Missing is the zero View**, the miss `Lookup` returns.
- **No allocation.** Both forms read the bytes in place
  (`TestCompareAllocatesNothing`). `StringLiteral` copies its text once at
  construction, so the comparison itself copies nothing.
- **A boolean is true only for 0x01**, as a decoder reads it. `Validate`
  refuses any byte but 0x00 and 0x01; a `TrustStored` document was not
  walked and could hold another.
- **The result is -1, 0 or 1**, never another magnitude.
- **Position does not break ties.** The sort breaks ties by position
  (spec 6.3); the comparator sees two values, not their documents, so the
  tie-break is the caller's (a stable sort).

## Step 4b: mirrored `Value`, `Document`, `Element`, `Array`

`Value` has all 45 methods of the inventory (the typed accessors with their
`OK` forms, `AsInt32`, `AsInt64`, `Equal`, `IsNumber`, `Validate`, `String`,
`DebugString`), `Document` has its 9 (`Lookup`, `LookupErr`, `Index`,
`IndexErr`, `Elements`, `Values`, `Validate`, `String`, `DebugString`) and
`Element` its 10. `ReadDocument` and `ReadElement` are exported with the
vendor's signatures. Four files are ported from the vendor's
`value.go`, `document.go`, `element.go`, `array.go`, and `read.go` and
`render.go` are ported from parts of the others (`docs/ported-files.md`).

### Decisions the spec left open

- **`Array` is exported though the inventory does not list it.** `Value.Array`
  and `Value.ArrayOK` return it, and a listed type is mirrored whole (spec 5.1
  item 2). It carries the vendor's six methods. `TestBsoncoreExportsOnlyInventoriedNames`
  allows exactly this name, with the reason, and fails if the allowance is
  stale. Nothing else off the inventory is exported.
- **The vendor's error types and sentinels are unexported.** `ElementTypeError`,
  `InsufficientBytesError`, `ValidationError`, `ErrElementNotFound`, `ErrEmptyKey`
  and the others are not in the inventory. Each is an unexported type or value
  with the vendor's `Error()` text, and the tests compare the text. A caller that
  matched one by type or by `==` would not compile, and no call site in the
  inventory does.
- **The typed accessors panic as the vendor's do (S-4).** A wrong type or short
  data panics with an error whose text is the vendor's (`Call of
  bsoncore.Value.Double on Boolean type`, `too few bytes to read next
  component`). Each panic site carries a comment naming the contract and its
  `OK` form. `Document.Index` and `Array.Index` panic on the error `IndexErr`
  returns.
- **The ceiling on the renderings.** `String` and `DebugString` of a `Value`,
  `Element`, `Document` or `Array` open one level per document, array or
  code-with-scope scope, the root being level 1. At the 201st level they return
  the refusal these methods already use for input they cannot render: `""` for
  `String`, `"<malformed>"` for `DebugString`, for the whole result (no partial
  text). `bsoncore` cannot import `bson`, so the refusal is not
  `bson.ErrNestingTooDeep`; `bson.Raw.String` (step 4a) states the sentinel
  itself by checking its depth first.
- **`Element.DebugString` writes the value with `String`, not `DebugString`,**
  as the vendor's `%v` does. Kept, because the output is the contract.
- **The string escaping is written fresh.** The vendor's `escapeString` uses
  `tables.go`, which derives from Go's `encoding/json` (S-3). The library
  escapes by rule and `TestBsoncoreStringEscapesLikeTheVendor` compares every
  byte and every rune below U+3000.

## Step 5b core: the Extended JSON reader

Package `github.com/hoarddb/hoarddb-bson/internal/extjson`, internal until
step 4f puts `bson.UnmarshalExtJSON` in front of it:

```go
func AppendDocument(dst, text []byte, canonical bool) ([]byte, error)

type Reader struct{ /* unexported */ }
func NewReader(text []byte, canonical bool) (*Reader, bsontype.Type, error)
func (reader *Reader) ReadElement(inScope bool) (string, bsontype.Type, error)
func (reader *Reader) ReadArrayValue() (bsontype.Type, error)
func (reader *Reader) ReadCodeWithScope() (string, error)
func (reader *Reader) ReadScalar(valueType bsontype.Type) (Scalar, error)
type Scalar struct{ Type bsontype.Type; Text, Options string; Double float64; Int64 int64; ... }

const MaxNestingDepth = 200 // == bson.MaxNestingCeiling
var ErrInvalidJSON, ErrEndOfDocument, ErrEndOfArray error
```

### What it is held to

`AppendDocument` equals the vendor's `bson.UnmarshalExtJSON(text,
canonical, &raw)` into a `bson.Raw`: the same acceptance, the same
`err.Error()`, the same bytes, and on a refusal the same bytes left in the
target (`oracle/extjson_read_test.go`, `oracle/extjson_errors_test.go`,
`FuzzExtJSONDifferential`). The error table,
`oracle/testdata/extjson-error-table.json`, is generated from the vendor by
`go test -run TestExtJSONErrorTextMatchesVendor -update-extjson-error-table`,
and the test fails when the committed table is not what the vendor
generates now.

### Decisions the spec left open

- **Derived, except the scanner** (coordinator's ruling, 2026-10-24).
  `parser.go`, `reader.go`, `wrappers.go` and `extjson.go` follow the
  vendor's `bsonrw` parser, reader, wrapper readers, copier and value
  writer (Apache-2.0, apache header form), so they carry the vendor's
  header and a `Modified by hoardDB` line and are listed in
  `docs/ported-files.md`. None of them derives from Go's `encoding/json`.
  The scanner, the one vendor file that does (`json_scanner.go`), is written
  fresh (build doc S-3) and keeps the hoardDB header. Every branch the
  vendor's parser has is kept, also the ones the reader cannot reach, so
  the equivalence can be checked one branch at a time.
- **Error positions restart every 512 bytes**, as the vendor's do: its
  scanner counts from the start of each 512-byte read.
- **Arrays do not count toward the ceiling of 200**, as in the vendor. The
  byte writer nests without recursion, so 100,000 nested arrays cost frames
  on the heap, not the goroutine stack.
- **The root is read once and never as a wrapper.** An unreadable first
  value is `ErrInvalidJSON` ("invalid JSON input"), all the vendor says.
- **Nothing after the root document is read** ("stops after the first JSON
  value"), and a repeated key is written twice. Both are quirks the spec
  makes rows of the error table.

### The seam for the Go-value half

The decode into `*map[string]interface{}`, `*bson.M` and `*bson.D` (step
5b's second half, after 4e/4f) must be driven by `Reader`, not by decoding
the bytes `AppendDocument` writes. The vendor reads Go-value targets from
the same value reader, without writing bytes, and the table shows four
places where bytes would give the wrong answer:

1. **A zero byte in a key, or in a regex pattern or options**, is refused
   only by the byte writer ("BSON element key cannot contain null bytes",
   "BSON regex values cannot contain null bytes"). The Go-value targets
   accept it. `Reader` does not refuse it; `AppendDocument` does.
2. **Regex options are sorted only by the byte writer.** A map receives
   `primitive.Regex{Options: "xmi"}` as typed. `Scalar.Options` is the
   input order; `AppendDocument` sorts.
3. **The error text differs by target** where the error is not the
   reader's. For an error inside an element's value, the raw and `bson.D`
   targets return the reader's text, and the map and `bson.M` targets
   prefix it with `error decoding key <dotted path>: ` (inside a code with
   scope's scope, `bson.D` prefixes it too). A root that is not a document
   gets each target's own "cannot decode X into a Y" text, and a `null`
   root is accepted by every Go-value target and refused by the raw one.
4. **A partly filled target.** The vendor's map decode fills the map as it
   reads, so an error leaves the keys read before it. The vendor's raw
   target keeps the bytes written before the error, which `AppendDocument`
   returns too. Whether `bson.UnmarshalExtJSON` must reproduce either is
   the Go-value half's call, recorded there.

The Go-value half builds each Go value from `ReadScalar`'s `Scalar` and
from `ReadElement`/`ReadArrayValue`, applying the decode rules of spec 5.3
(D1 to D7) and the wrapping of item 3. The bytes path stays
`AppendDocument`, which `bson.UnmarshalExtJSON` uses for a `*bson.Raw`
target.

## Step 4a: `Document.Raw`, and the renderings' ceiling

Package `github.com/hoarddb/hoarddb-bson/bson`:

```go
func (document Document) Raw() (Raw, error)
```

The mirrored `Raw`, `RawElement` and `RawValue`, and `D`, `E`, `M`, `A`
(aliases of the `primitive` types), are listed in the inventory, not here.

### Decisions the spec left open

- **`Raw` returns an error.** Spec 5.2 writes `Raw() bson.Raw`; step 2a's rule
  above (every method that reads a Document refuses the zero value with an
  error, and names `Raw`) needs an error result to keep. The zero Document
  gives `nil` and an error matching `ErrZeroDocument`. Any other Document
  gives its own bytes, uncopied, with the capacity cut at their end, so an
  `append` to the `Raw` copies instead of writing past the document. This
  is a departure from the spec's signature, reported for a ruling.
- **No conversion from `Raw` to `Document`.** Bytes still reach a `Document`
  only through `Validate`, `TrustStored` or the builder (call C1);
  `bson.Document(raw)` does not compile (`TestRawDoesNotConvertToADocument`).
- **The renderings' ceiling.** `Raw.String`, `RawValue.String`,
  `RawValue.DebugString` and `RawElement.DebugString` render as the vendor's
  do up to 200 levels. Past that they return
  `<bson: document nested too deep: more than 200 levels>`, the text of
  `ErrNestingTooDeep` and the ceiling, for the whole result. They call
  bsoncore's renderer first, and measure the depth only when it gave its own
  refusal (`""` or `"<malformed>"`): the measure reads the bytes the way the
  renderer does and stops at level 201, so it recurses at most 201 frames.
  A document that is both deeper than 200 and unreadable further on renders
  the nesting refusal, where the vendor renders `""`.
- **The panicking accessors** of `RawValue` (20) and `Raw.Index` call bsoncore's,
  which panic as the vendor's do (S-4); each carries the contract note. The
  `bson` package adds no `panic` call of its own.
- **Left for later steps:** `RawValue.Unmarshal`, `UnmarshalWithRegistry` and
  `UnmarshalWithContext`, and the vendor's unexported registry field, wait for
  the decoder (4f). `RawElement.String` renders through `MarshalExtJSON`, the
  Extended JSON writer of 5a, and waits for it.

## Step 4c: `internal/plan`, the per-type plan (internal)

Package `github.com/hoarddb/hoarddb-bson/internal/plan` is not part of the
module's API; steps 4d and 4e build on it. Recorded here for them.

```go
func NewCache(bsonTypes BSONTypes) *Cache
type BSONTypes struct{ Raw, RawValue reflect.Type }
func (cache *Cache) Lookup(goType reflect.Type) (*Type, error) // ErrNilType for nil
type Type struct {
	GoType                       reflect.Type
	Encode, Decode               Choice   // the vendor registry's codec
	Zeroer                       bool     // GoType implements IsZero() bool
	Elem                         *Type    // pointer, slice, array, map
	KeyEncode, KeyDecode         KeyCodec // map key rules
	ElemIsByte, ElemIsE, ConvertibleToD bool
	Struct                       *Struct  // struct layout, nil when Err
	Err                          error    // the vendor's refusal of the layout
}
type Choice struct{ Codec Codec; AddressedOnly bool; Otherwise Codec }
type Struct struct{ Fields []Field; InlineMap *Type; InlineMapIndex int }
func (layout *Struct) FieldByKey(key string) (*Field, bool)
type Field struct{ Key string; Index []int; ThroughPointer, OmitEmpty, MinSize, Truncate bool; Type *Type }
```

### Decisions the spec left open

- **The special-type table is here, not in 4d.** Which codec a type gets is
  a per-type decision, and the oracle compares it with the vendor's
  `NewRegistry().LookupEncoder`/`LookupDecoder` directly. It holds every
  type codec of the vendor's default registry: `[]byte`, `time.Time`,
  `interface{}`, `bsoncore.Array` and `Document`, the `primitive` scalars and
  legacy types, `Decimal128`, `json.Number`, `url.URL`, `bson.Raw`,
  `bson.RawValue`, and `primitive.D` for decode only (it encodes through the
  slice codec). `bson.Raw` and `bson.RawValue` come in through `BSONTypes`,
  because package `bson` will import this package.
- **One plan per type, eager.** A miss compiles the type and every type it
  reaches through exported fields and elements, in a session private to the
  goroutine, and publishes them all at the end with `LoadOrStore`. A
  recursive type gets its own plan back, created empty and filled before
  anything is published. The vendor describes structs lazily; the results
  are the same, and a bad struct's error stays on that struct's plan, so a
  struct that only points at it compiles (the vendor errs only when it
  reaches a value of it).
- **Errors are cached.** The vendor re-describes a refused struct on every
  call; the plan keeps the refusal. The text is the vendor's.
- **A struct's layout comes before its field types.** Layouts depend only
  on the inline structure and are built, and memoized per session, before
  any field type is planned, so a struct reached back through an ordinary
  field is never inlined half-built, and the result does not depend on
  lookup order.
- **A struct that inlines itself is an error** (`(struct T) inlines a
  struct whose layout includes itself`), detected along the chain of inline
  descents only. The vendor recurses until the stack overflows, so there is
  nothing to match.
- **Hooks are interfaces by method set** (`Marshaler`, `ValueMarshaler`,
  `Proxy`, `Unmarshaler`, `ValueUnmarshaler`, `Zeroer`, `KeyMarshaler`,
  `KeyUnmarshaler`), declared in the package with the library's `bsontype`.
  None of them is in the inventory, so `bson` does not export them.
- **What stays at run time** for 4d and 4e: the dynamic type of an interface
  value (a non-empty interface type has `NoCodec` and is dispatched on its
  dynamic type, as the vendor does), nil checks, the decode ancestor (rows
  D1 to D7), and addressability, which `Choice.AddressedOnly` asks about.

## Step 4d: `internal/encode`, the reflection encoder (internal)

Package `github.com/hoarddb/hoarddb-bson/internal/encode` is not part of
the module's API. Step 4f's `bson.Marshal` is an entry over it.

```go
func New(cache *plan.Cache) *Encoder
func (encoder *Encoder) Document(value any) ([]byte, error) // the vendor's bson.Marshal, map keys sorted
// internal/plan gains one mutable field:
type Type struct{ ...; SizeHint atomic.Int32 } // last top-level encoded size of the type
```

### Decisions the spec left open

- **Placement.** The encoder is a package of its own, beside
  `internal/plan`, so the oracle module can call it before `Marshal`
  exists (step 4f) without `bson` exporting a name the inventory does not
  list. It takes its plan cache from the caller: `bson` declares the one
  cache, with `plan.BSONTypes{Raw, RawValue}`, in 4f.
- **One writer, positions passed down.** The vendor's value writer keeps a
  mode stack; here each value is written at a slot (top level, document
  element, array element), and the top level refuses every write but a
  document with the vendor's `TransitionError` text, naming the writer
  method the vendor would have called (`WriteBinaryWithSubtype` for
  `[]byte`, `WriteValueBytes` for a hook, `WriteJavascript` with its
  lower-case s). A code-with-scope's scope is encoded at a fresh top level,
  as the vendor does it.
- **Map order (O11).** A map's entries are written in ascending byte order
  of their *encoded* keys (so `map[int]` keys sort as text: `"10" < "9"`),
  at every level, the inline map's entries after the struct's fields. Every
  key is encoded before any value is written; where more than one entry
  fails, the error is one the vendor can return in some map order. Two
  distinct keys that encode to the same text (two hook keys returning the
  same string) are still written in Go's map order.
- **Errors.** The text is the vendor's; the types are the library's own,
  unexported. Step 4f decides which error types `bson` exports.
- **Where the vendor panics, an error.** A top-level `MarshalBSON` result
  declaring fewer than 4 bytes; a raw value length that overflows int32
  (`bsoncore`'s `valueLength`); `ProxyBSON` returning a nil pointer; a value
  method (`MarshalBSON`, `MarshalBSONValue`, `ProxyBSON`, `MarshalKey`,
  `MarshalText`) reached through a nil pointer at the top level, in a hook
  interface, or as an interface map key. `TestEncodeRefusesWhereTheVendorPanics`
  lists them.
- **The pointer codec's error.** For a pointer whose element type has no
  codec, the vendor names the element type the first time and the pointer
  type after that (its `PointerCodec.ecache`); the library always gives the
  first text.
- **`IsZero` is asked only under omitempty.** The vendor asks it for every
  field and uses the answer only under omitempty, so the bytes are the
  same; a field's `IsZero` is simply not called when its answer is unused.
- **Pool and hint (spec 5.5).** A `sync.Pool` of writers; the encoding is
  copied out, so no caller ever holds a pooled buffer; a buffer over 16 MiB
  is not pooled. After a top-level encode the type's plan stores the
  encoded size, and the next encode of that type starts with at least that
  capacity when the pooled buffer is smaller. A top-level `MarshalBSON`
  value is copied without a hint.
- **No depth ceiling here.** The encoder recurses as the vendor does; a
  pointer cycle overflows the stack in both. The 200 ceiling on encode is
  step 4f's.

## Step 5a core: the Extended JSON writer

Package `github.com/hoarddb/hoarddb-bson/internal/extjson`, internal until
the step after 4f puts `bson.MarshalExtJSON` in front of it:

```go
func AppendText(dst, document []byte, canonical, escapeHTML bool) ([]byte, error)
func AppendElementText(dst, element []byte, canonical, escapeHTML bool) ([]byte, error)
var ErrNestingTooDeep error // "bson: document nested too deep"
```

Package `bson` gains `RawElement.String`, which renders through
`AppendElementText` (canonical, no HTML escaping, as the vendor's does).

### What it is held to

`AppendText` equals the vendor's `bson.MarshalExtJSON(bson.Raw(document),
canonical, escapeHTML)`: the same text byte for byte and the same
`err.Error()`, in all four modes, over the corpus, the vectors, the v1.0
corpus, generated documents and `FuzzExtJSONWriterDifferential`
(`oracle/extjson_write_test.go`). `RawElement.String` equals the vendor's.
The two exceptions are below.

### Decisions the spec left open

- **The ceiling.** A level past 200 is refused with an error matching
  `ErrNestingTooDeep` (`bson: document nested too deep: more than 200
  levels`); the vendor writes any depth. Documents, arrays and
  code-with-scope scopes count, the root as level 1, as `Validate` and the
  4a renderings count. The refusal comes where the vendor would open that
  level, after it has read the container's header, so an error the vendor
  meets first is still the vendor's. The oracle settles "would open" with
  the vendor's own walk (its `Copier` into a writer that counts levels).
  The writer recurses one frame per level, so at most 201 frames.
- **Where the vendor panics, an error.** A root declaring a length of 0 to
  3 (`declared document length %d is shorter than its own length field`,
  the 4d encoder's text) and a value length that overflows int32 (`not
  enough bytes available to read type ...`). `RawElement.String` renders
  `<malformed>` for both, where the vendor's panics.
- **On an error `AppendText` returns `dst` as it was**, as the vendor returns
  no text.
- **`RawElement.String` counts the element's value as level 1**, as
  `RawElement.DebugString` does, although the text includes the one-element
  document the vendor wraps around it. Past the ceiling it renders the 4a
  refusal `<bson: document nested too deep: more than 200 levels>`; any
  other error renders `<malformed>`, as the vendor's does.
- **Derived, except the escaping.** `writer.go` (the text of each value,
  from `extjson_writer.go`) and `copy.go` (the reads, from `copier.go` and
  `value_reader.go`) carry the vendor's header and are listed in
  `docs/ported-files.md`. `escape.go` is written fresh (build doc S-3) and
  keeps the hoardDB header; its rules are stated in its header comment.
- **The value reader is the vendor's, quirks kept.** Below the root, each
  value is read sequentially and a container's declared length is checked
  only at its terminator, so a container that declares too short a length
  is read on into the bytes after it, and error offsets count from the
  start of the root element's value. A DBPointer's namespace is written
  unescaped; regex options are sorted by rune after a `[]rune` conversion,
  so a byte that is not UTF-8 becomes U+FFFD there and nowhere else.

### The seam for the Go-value wrapper

`bson.MarshalExtJSON(value, canonical, escapeHTML)` (after 4f) is thin only
for the values the vendor copies from bytes onto its Extended JSON writer:
`bson.Raw`, `bsoncore.Document` and a top-level `Marshaler` go straight to
`AppendText` (the vendor's `CopyDocumentFromBytes`). The wrapper maps
`extjson.ErrNestingTooDeep` to an error matching `bson.ErrNestingTooDeep`.

Every other Go value must be encoded **onto the text writer**, not encoded
to BSON by 4d's encoder and then written with `AppendText`. Bytes in
between change the answer in four places:

1. The BSON value writer refuses a zero byte in a key or a regex and a
   document over its size limit; the Extended JSON writer does not (it
   writes `\u0000`), so `MarshalExtJSON` of `bson.M{"a\x00b": 1}` succeeds
   in the vendor.
2. Nested byte-backed values are copied by the vendor from their own
   bytes: `bson.Raw` and `bsoncore.Document` fields (`CopyDocumentFromBytes`),
   `bsoncore.Array` (`CopyArrayFromBytes`), `RawValue`, `MarshalBSONValue`
   and nested `MarshalBSON` results (`CopyValueFromBytes`). Their error
   offsets count from the start of those bytes, and the root-level checks
   (`couldn't read length`, `invalid key found`, ...) apply at their start.
   After a BSON round trip they would count from the enclosing root element.
3. A top-level value that is not a document fails on the Extended JSON
   writer's mode check, with its `TransitionError` text (action "write"),
   which the text writer does not produce yet.
4. The vendor's map order is random; the encoder's sorted order (O11)
   applies, and the oracle compares map levels by element set (spec 6.1),
   which is `TestMarshalExtJSONMapsCompareByElementSet`, not built here.

So the wrapper's work is: have 4d's encoder write through a small sink
interface that both its BSON writer and `textWriter` implement.
`textWriter`'s methods are already the vendor value writer's operations
(`openDocument`, `key`, `closeDocument`, `openArray`, `closeArray`,
`openScope`, `closeScope`, and one `write*` per scalar), and the three
byte-copy entry points the vendor uses are `copyRoot`'s body at a nested
slot (`CopyDocumentFromBytes`, and the same loop for `CopyArrayFromBytes`)
and `valueReader.copyValue` on a fresh reader (`CopyValueFromBytes`). The
mode checks of item 3 are added to the sink, with the vendor's text.

## Step 4e: `internal/decode`, the reflection decoder (internal)

Package `github.com/hoarddb/hoarddb-bson/internal/decode` is not part of the
module's API. Step 4f puts `bson.Unmarshal`, `RawValue.Unmarshal` and
`Document.Decode` in front of it, with the depth ceiling. Recorded here for
4f.

```go
func New(cache *plan.Cache) *Decoder
type Decoder struct{ /* the cache, and interface{}'s and primitive.D's plans and the type map, built once on first use */ }
func (decoder *Decoder) Unmarshal(documentBytes []byte, target any) error                          // the vendor's bson.Unmarshal
func (decoder *Decoder) UnmarshalValue(valueType bsontype.Type, valueBytes []byte, target any) error // the vendor's RawValue.Unmarshal
```

### What it is held to

For every input and target the oracle tries, the outcome, the error text and
the decoded value (Go types inside `interface{}` holders included, and the
partly filled target after an error) equal the vendor's `bson.Unmarshal`, or
`RawValue.Unmarshal` for `UnmarshalValue`: the contract-table rows D1 to D7,
I4, T2, B1, P1, N4, N5, R1 and E1, the corpus, the vectors, the v1.0-written
corpus, malformed input per target and `FuzzDecodeDifferential`
(`oracle/decode_*_test.go`).

### Decisions the spec left open

- **A port, not a rewrite.** `reader.go`, `errors.go`, `decoder.go`,
  `values.go` and `containers.go` follow the vendor's value reader and default
  decoders (Apache-2.0, apache header form; `docs/ported-files.md`). The order
  of reads and checks is the vendor's, because that order decides which error
  a malformed input gets, and the text is part of the contract (spec 5.1). The
  registry is replaced by the plan's `Codec`, the `bsonrw.ValueReader`
  interface by one concrete reader on the caller's stack with 16 frames
  inline, and the decode context by the two fields the default registry
  reads: the ancestor and `Truncate`.
- **`New` takes the cache, as `encode.New` does.** The plan cache's
  `BSONTypes` must be package bson's `Raw` and `RawValue`, so the decoder
  cannot build its own; step 4f declares the one cache and hands it to both
  `encode.New` and `decode.New`. The decoder's tables (the plans of
  `interface{}` and of the type map's Go types) are looked up once, on the
  first decode, and a lookup error (only a nil type gives one) is returned by
  that decode and every later one.
- **Type decoders and in-place decodes.** Where the vendor decodes a map's
  or a `bson.D`'s element through a type decoder
  (`decodeTypeOrValueWithInfo`), the library does too: a new value of the
  codec's type, converted to the element type. An array's values are decoded
  in place into the zero elements of a new slice of the target's type, and a
  DateTime, ObjectID or `time.Time` is set in place; the vendor boxes each
  into a new `reflect.Value`. The outcomes are the same (a fresh zero value
  either way); the allocations are fewer.
- **Struct keys are looked up by their bytes** (`plan.Struct.FieldByKeyBytes`,
  added beside `FieldByKey` in 4e), so a key the decoder only looks up is not
  copied into a string. The lower-cased fallback allocates as the vendor's
  does.
- **A failed read sets nothing.** A scalar decoder sets its target only after
  a successful read, as the vendor does, so a prefilled target keeps its value
  when its element is refused.
- **Error types are unexported**, none being in the inventory: the vendor's
  `DecodeError`, `ValueDecoderError`, `ErrNoDecoder`, `ErrNoTypeMapEntry`,
  `ErrDecodeToNil` and the reader's `TransitionError` each have an unexported
  mirror with the vendor's `Error()` text and unwrap chain. The key path of a
  nested error is the vendor's (`error decoding key a.l.1.n: ...`), including
  where it has none: an element of a top-level `bson.D` and an inline map
  value are not wrapped with their key.
- **The type codecs outside the table** (step 4c report, section 11 item 3):
  `json.Number` decodes from a double, an int32 or an int64, `url.URL` from a
  string through `url.Parse`, as the vendor's do.

### Where the vendor panics or never returns

The library returns an error in each case. The first is the coordinator's
ruling (2026-10-24); the others are reported in
`docs/build-reports/step-4e.md` for a ruling. Each is pinned by
`oracle/decode_divergence_test.go`, which recovers the vendor's panic (or, for
the loop, does not call the vendor) and asserts the library's text.

| Input | Vendor | Library's error |
| --- | --- | --- |
| A key of a struct inlined by value inside a struct inlined through a nil pointer (`struct{Q *T2 ",inline"}`, `T2{D3 T3 ",inline"}`, a key of `T3`) | panics in `getInlineField`: `reflect: Elem of invalid type T3` | `decoding key "<key>": the inlined struct T3 in <outer> is reached through a nil pointer and cannot be allocated; allocate the pointer before decoding` |
| A negative length where the value's bytes are copied (`bson.Raw`, `bson.RawValue`, `bsoncore.Document`, `bsoncore.Array`, the hooks; a negative root length into `bson.Raw`) | panics slicing: `slice bounds out of range` | `invalid length: <n>`, the reader's own text for a negative length, wrapped with the key as any field error is |
| A negative length on a skipped value that moves the read before byte 0 | panics on the next read: `index out of range` | `document is invalid: a value length moves the read to byte <n>, before the start of the bytes` |
| A negative length on a skipped value that returns the read to an element read before | reads the same elements for ever | `document is invalid: a negative value length returns the read to byte <n> again, so the document never ends` |
| A pointer to a type with no decoder (`*io.Reader`), decoded a second time in the process | the first decode returns `no decoder found for io.Reader`; every later one panics on the cached nil decoder | the first decode's error, every time |
| `UnmarshalValue` (`RawValue.Unmarshal`) into a nil pointer | panics: `reflect: call of reflect.Value.Type on zero Value` | `cannot Decode to nil value`, `Unmarshal`'s text for the same mistake |

A negative skip that moves backwards without either outcome is followed, as
the vendor follows it; only a revisit is refused. The struct decoder records
element offsets only after a backward skip, so a document without one pays
nothing for the check.

## Step 4f: the entry points, the ceiling and the oracle tap

Package `github.com/hoarddb/hoarddb-bson/bson`, beside the mirrored
`Marshal`, `MarshalValue`, `Unmarshal` and `RawValue.Unmarshal` (listed in
the inventory, with the vendor's signatures):

```go
func (document Document) Decode(target any) error

// Only with the bsonoracle build tag (tap.go):
type OracleEvent struct {
	Function string        // "Marshal", "MarshalValue", "Unmarshal", "RawValue.Unmarshal", "Document.Decode"
	Value    any           // the value encoded, or the decode's target
	Bytes    []byte        // the decode's input; the encode's result (after only)
	Type     bsontype.Type // MarshalValue's result type, RawValue.Unmarshal's value type
	Err      error         // after only
}
type OracleTap func(before OracleEvent) (after func(OracleEvent) error)
func SetOracleTap(tap OracleTap)
```

`MaxNestingCeiling` and `ErrNestingTooDeep` (step 2a) are now
`internal/nesting`'s `Ceiling` and `ErrTooDeep`, so the encoder and the
decoder, which `bson` imports, refuse with the same sentinel.

### Decisions the spec left open

- **One plan cache.** `bson` declares it with `plan.BSONTypes{Raw,
  RawValue}` and hands it to `encode.New` and `decode.New`; no other cache
  exists in the library. The oracle's harnesses call the entry points, so
  every contract test of 4d and 4e runs on that cache.
- **`MarshalValue`** is a new `encode.Encoder.Value`: the value written at
  an element with an empty key, as the vendor's
  `MarshalValueAppendWithContext` writes it, then the type byte and the bytes
  after the key's terminator, copied out of the pooled buffer. So a nil map,
  slice or pointer is a null, a scalar is allowed, `MarshalBSON` is checked
  first and its bytes go through the copier, and `MarshalBSONValue` writes
  any type it returns, 0 included.
- **The ceiling counts what recurses** (spec 5.2, call C2). Encode: every
  document, array and code-with-scope scope the encoder opens, the root as
  level 1 (`MarshalValue`'s value as level 1, as `RawValue` counts it); the
  201st is refused with an error matching `ErrNestingTooDeep`
  (`bson: document nested too deep: more than 200 levels`), after its header
  is written, so a header error is the vendor's. Bytes copied as they are
  (`Raw`, `RawValue`, `bsoncore` fields, a nested `MarshalBSON` result) open
  no level, because the vendor copies them without walking them. A value that
  contains itself is refused at level 201, where the vendor overflows the
  goroutine stack (`TestMarshalRefusesACycle`, ruling O17). Decode: every
  document, array and scope the decoder's reader pushes, refused once the
  container's header (and for a scope, the code-with-scope's length check)
  is read, so an error the vendor meets first is the vendor's. A value the
  decoder skips (a key the struct does not declare, row N5) or copies as bytes
  (`Raw`, `RawValue`, `bsoncore.Document`, a hook) pushes nothing and is not
  counted, so `Unmarshal` of 201 levels into one of those succeeds as the
  vendor's does. Nothing those targets hold is decoded until a later call,
  which applies the ceiling again.
- **`Document.Decode`** is `Unmarshal` over the document's bytes: no second
  validation, so it refuses only what `Unmarshal` would. The zero `Document`
  is refused with `ErrZeroDocument`, the target left as it was.
- **Top level** is the vendor's (spec Z1, as corrected by the 4d report): a
  nil pointer, slice, `D` or `[]byte` is refused with the vendor's
  `TransitionError` text, a nil map is the empty document, a nil `Raw` or
  `bsoncore.Document` and a zero `RawValue` are errors.
- **Error types.** The inventory lists no codec error type or sentinel that
  the tree matches with `errors.Is` or `errors.As` (its section 5 has no
  comparison row for `Marshal`, `Unmarshal`, `MarshalValue` or
  `RawValue.Unmarshal`), so `bson` exports none of the vendor's
  (`ErrDecodeToNil`, `ErrNilRegistry`, ...). Their texts are the encoder's
  and decoder's, which are the vendor's. The one sentinel `bson` adds to them
  is `ErrNestingTooDeep`, under every ceiling refusal, through the decoder's
  key-path wrapping too.
- **Kept out, with the reason** (`oracle/bson_inventory_test.go`):
  `Decoder`, `Encoder`, `NewDecoder`, `NewEncoder`, `NewRegistry`,
  `DefaultRegistry`, and `RawValue`'s `UnmarshalWithRegistry` and
  `UnmarshalWithContext`. The inventory reaches the first six only from
  `server/range_raw_test.go`, which spec call C15 rewrites before the swap,
  and the test-only vendor shim, and has no reference to the two methods.
  Their signatures name `bsoncodec.Registry`, `DecodeContext`,
  `EncodeContext` and `bsonrw`'s reader and writer, which call C7 leaves
  out, so none of them could keep the vendor's signature.
- **The tap** (spec 9.1.7, call C9) exists only with the `bsonoracle` tag.
  Each entry point calls the installed tap with its inputs before it runs
  (a decode's target not yet written, so the tap can copy a prefilled
  target for the vendor) and with its results after. An error from the
  `after` function is returned by the entry point instead of its own
  result: that is how a divergence fails the test that made the call. A
  call the tap makes, and a `MarshalBSON` that calls `Marshal`, are tapped
  too. Without the tag, `oracleTapCompiled` is the constant false, so the
  tapped branches are compiled away and no tap symbol is linked
  (`go tool nm`, step 4f report). Recording the (Go type, bytes) pairs to
  `$BSONORACLE_OUT` is the hoardDB shim's job, from the events' `Value` and
  `Bytes`.

## Step 5 (Go values): `MarshalExtJSON` and `UnmarshalExtJSON`

Package `github.com/hoarddb/hoarddb-bson/bson`, beside the mirrored
`MarshalExtJSON` and `UnmarshalExtJSON` (listed in the inventory, with the
vendor's signatures), the tap's events gain the Extended JSON calls:

```go
type OracleEvent struct {
	// ... as in step 4f; Function is also "MarshalExtJSON" or "UnmarshalExtJSON"
	Canonical  bool // the call's canonical argument
	EscapeHTML bool // MarshalExtJSON's escapeHTML argument
}
```

Package `internal/encode` gains `Encoder.Text(value any, canonical,
escapeHTML bool) ([]byte, error)`, package `internal/decode` gains
`Decoder.UnmarshalText(text []byte, canonical bool, target any) error`, and
package `internal/extjson` gains the two seams they drive:

```go
type TextWriter struct{ /* the 5a text writer and a stack of open level kinds */ }
func (text *TextWriter) Reset(canonical, escapeHTML bool)
func (text *TextWriter) Key(name string)
func (text *TextWriter) OpenDocument() error // also OpenArray, OpenScope(code); Close closes the innermost
func (text *TextWriter) CopyDocument(documentBytes []byte) error // CopyArray, CopyScopeElements, CopyValue(type, bytes, topLevel)
func (text *TextWriter) WriteInt32(number int32) // one Write per scalar type, as the vendor's value writer
func TopLevelWriteError(method, destination string) error

type ValueReader struct{ /* the vendor's extJSONValueReader frames over the 5b parser */ }
func NewValueReader(text []byte, canonical bool) (*ValueReader, error)
// Type, Skip, ReadArray, ReadDocument, ReadCodeWithScope, ReadElement, ReadValue,
// one Read per scalar type, AppendDocumentBytes, AppendArrayBytes, AppendValueBytes
var ErrSkipNeverEnds error // "the value the decoder skips never ends"
```

### What it is held to

`MarshalExtJSON` equals the vendor's on every Go value the oracle tries,
in all four modes: the text compared by `compare.ExtJSON` (spec 6.1's map
rule applied to text: map levels as sets of members, everything else byte
for byte, keys as written) and the error text exactly. That is every encode
case of steps 4d and 4f (`requireEncodesLikeVendor` runs each through
`MarshalExtJSON` too), the seam cases of `extJSONGoValueCases`, and
`FuzzMarshalExtJSONGoValues`. `UnmarshalExtJSON` equals the vendor's into
41 targets (every target of the step 4e tests, the CLI's four, byte-backed
fields, hooks, typed structs, prefilled targets, and the scalar and slice
roots a text can have) over the corpus texts, the error table and the
Go-value texts, in both modes: the outcome, the error text, and the decoded
value after an error too; and into the map, `bson.M` and `bson.D` columns of
the vendor-generated error table (`TestUnmarshalExtJSONGoValueErrorTextMatchesTheTable`,
`TestExtJSONErrorTextAtCLISites`), and `FuzzUnmarshalExtJSONGoValues`.

### Decisions the spec left open

- **One walk, a text mode, no sink interface.** The 5a core suggested
  that the encoder write through an interface both writers implement. The
  encoder instead keeps one concrete writer with a `textMode` flag: each
  write branches once on it, and the BSON path keeps its direct appends, its
  inlining and its allocation count (the step 4d and 4f allocation tests
  are unchanged). The decoder does the same: its concrete reader holds an
  `*extjson.ValueReader`, and every read branches once on it being set, so
  the decoders of step 4e run unchanged over text and the error text, the
  key paths and the partly filled target follow from the vendor's own read
  order.
- **What the text writer does not refuse.** A zero byte in a key or a
  regex, and a size past MaxInt32, are refused only by the BSON writer, so
  not in text mode. A code with scope's scope is still encoded to BSON at a
  fresh top level and then copied, as the vendor does it, so its zero-byte
  refusals stay.
- **Copied bytes.** `bson.Raw`, `bsoncore.Document` and a top-level
  `MarshalBSON` result are copied with the root checks of their own bytes
  (`CopyDocumentFromBytes`), `bsoncore.Array` likewise (`CopyArrayFromBytes`),
  and `RawValue`, `MarshalBSONValue` and a nested `MarshalBSON` result by a
  fresh value reader (`CopyValueFromBytes`), whose offsets start at those
  bytes. At the top level such a value is copied when it is a document and
  refused with the writer's transition error otherwise, after it is read as
  far as the vendor reads it.
- **Map order (O11)** is the encoder's: ascending byte order of the encoded
  keys, at every level.
- **The ceiling on write** counts every level the text writer opens,
  copied bytes included, so `MarshalExtJSON` of a `bson.Raw` 201 levels deep
  is refused as `AppendText` refuses it; a value that contains itself is
  refused at level 201 (ruling O17).
- **The ceiling on read** counts every document, array and scope the
  decoder descends into, the root as level 1, as `Unmarshal` does, so 201
  levels with arrays among them are refused with `nesting.ErrPastCeiling`
  where the vendor decodes them. The parser's own limit of 200 objects stays
  the vendor's, with the vendor's text, and its refusal now matches
  `ErrNestingTooDeep` too (`extjson.ErrNestingTooDeep` is the same
  sentinel). A value copied as bytes (a `bson.Raw` field, a hook) descends
  without the decoder and opens no level, as in step 4f.
- **A partly filled target is reproduced.** The 5b core left this to the
  Go-value half: the decode into a map keeps the keys read before an error,
  and a `bson.Raw` keeps the bytes written before it, as the vendor's do;
  the harness compares the targets after every error.
- **Where the vendor never returns, an error.** A struct skips an
  undeclared key with the vendor's `skipObject`, which loops for ever when
  the skipped value's text breaks or ends before it closes (the parser stops
  advancing). The library returns the parser's error wrapped with
  `ErrSkipNeverEnds` (`TestUnmarshalExtJSONVendorSkipNeverReturns` runs the
  vendor in a child process it kills). The oracle does not call the vendor
  on such an input; it requires that the text fails to read into a map,
  which never skips.
- **Where the vendor panics, an error.** A null text root decoded into a
  map passed by value: the vendor's map codec sets the unaddressable map to
  nil and panics; the library says to pass a pointer
  (`TestUnmarshalExtJSONRefusesWhereTheVendorPanics`).
- **`bsoncore.Array` from text always fails**, as the vendor's: its copy
  writes an array at a value writer's top level, which refuses it.
- **The tap** reports the two calls with their mode arguments, the text as
  the event's bytes.
