import "std/test.ll" as t

@test("multiline backtick string with interpolation and escapes")
bind test_multiline_backtick = () -> {
    let name = "World"
    let count = 42
    let s = `Hello, $name!
Count: ${count + 1}
JSON: {"key": "value"}
Backtick: \`literal\`
Dollar: \$100`

    let expected = "Hello, World!\nCount: 43\nJSON: {\"key\": \"value\"}\nBacktick: `literal`\nDollar: \$100"
    t.assert_eq(s, expected)
}

@test("json multiline formatting")
bind test_json_template = () -> {
    let raw_json = `{
  "items": [1, 2, 3],
  "status": "ok"
}`
    let expected = "{\n  \"items\": [1, 2, 3],\n  \"status\": \"ok\"\n}"
    t.assert_eq(raw_json, expected)
}
