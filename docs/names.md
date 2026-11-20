# Name origin

Every mirrored name and where it comes from (spec section 4.2 item 5). A name
used here is used because the BSON specification or an upstream library already
uses it, not as a claim about either.

| Name | Origin |
| --- | --- |
| `ObjectID` / ObjectId | spec-derived (bsonspec.org) |
| `DateTime` | spec-derived |
| `Timestamp` | spec-derived |
| `Decimal128` | spec-derived |
| `Binary` and its subtypes | spec-derived |
| `Regex` | spec-derived |
| `MinKey`, `MaxKey` | spec-derived |
| `DBPointer`, `CodeWithScope`, `Symbol`, `JavaScript`, `Undefined`, `Null` | spec-derived (bsonspec.org type names; the deprecated ones are kept for round-trip) |
| `M` | mgo-derived (gopkg.in/mgo.v2/bson) |
| `D` | mgo-derived |
| `Marshal` | mgo-derived |
| `Unmarshal` | mgo-derived |
| `Raw` | mgo-derived |
| `RawValue`, `RawElement` | vendor-original |
| `E` | vendor-original (the Go driver this library mirrors) |
| `A` | vendor-original |
| the `primitive` package | vendor-original |
| the `bsoncore` builder names | vendor-original |
| the `bsoncore` `Read*` names and `Value` | vendor-original |
| `bsoncore.Document`, `Element`, `Array` and their methods | vendor-original |
| `bsontype` | vendor-original |
| `StructTags` | vendor-original |
