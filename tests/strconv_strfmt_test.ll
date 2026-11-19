import "std/strings"
import "std/strconv"
import "std/strfmt" as fmt

bind main = (): {
    // 1. strconv methods on string
    let num_str = "12345"
    let n? = num_str.to_int()
    assert((n? ?? 0) == 12345)
    assert(num_str.is_int())
    assert(num_str.is_numeric())

    let hex_str = "FF"
    let h? = hex_str.to_int_radix(16)
    assert((h? ?? 0) == 255)
    assert(hex_str.is_hex())

    let bin_str = "1010"
    let b? = bin_str.to_int_radix(2)
    assert((b? ?? 0) == 10)
    assert(bin_str.is_bin())

    let float_str = "3.14159"
    let f? = float_str.to_float()
    assert(float_str.is_float())

    let bool_str = "true"
    let bl? = bool_str.to_bool()
    assert((bl? ?? false) == true)

    let bad_str = "not_a_number"
    assert(not bad_str.is_numeric())
    assert((bad_str.to_int() ?? -1) == -1)

    // 2. Formatting values to strings
    assert(strconv.int_to_hex(255, true) == "FF")
    assert(strconv.int_to_bin(10) == "1010")
    assert(strconv.int_to_oct(64) == "100")

    // 3. strfmt methods on string
    let raw = "hello"
    assert(raw.pad_left(8) == "   hello")
    assert(raw.pad_right(8) == "hello   ")
    assert(raw.center(9) == "  hello  ")
    assert(raw.repeat(3) == "hellohellohello")
    assert(raw.reverse() == "olleh")

    let long_text = "The quick brown fox jumps over the lazy dog"
    assert(long_text.truncate(13) == "The quick ...")

    // 4. Case conversions
    let mixed = "helloWorldExample"
    assert(mixed.to_upper() == "HELLOWORLDEXAMPLE")
    assert(mixed.to_lower() == "helloworldexample")
    assert(mixed.to_snake_case() == "hello_world_example")
    assert(mixed.to_kebab_case() == "hello-world-example")
    assert("hello_world_example".to_camel_case() == "helloWorldExample")
    assert("hello world".to_title() == "Hello World")

    // 5. Quote / Unquote
    let quoted = "hello \"world\"".quote()
    let unquoted? = quoted.unquote()
    assert((unquoted? ?? "") == "hello \"world\"")

    // 6. Occurrences
    assert("banana".count_occurrences("an") == 2)
    assert("banana".count_occurrences("na") == 2)
    assert("aaaa".count_occurrences("aa") == 2)

    // 7. Data formatters (both standalone and primitive method syntax)
    assert(fmt.format_int_commas(1234567) == "1,234,567")
    assert(fmt.format_float_commas(1234567.89, 2) == "1,234,567.89")
    assert(fmt.format_bytes(1024) == "1.00 KB")
    assert(fmt.format_bytes(1048576) == "1.00 MB")
    assert(fmt.format_bytes(5368709120) == "5.00 GB")
    assert(fmt.format_percent(0.8526, 1) == "85.3%")
    assert(fmt.format_currency(1234.5, "\$", 2) == "\$1,234.50")

    // 8. Methods on primitive float & int types
    let f = 1234567.89
    assert(f.format_float(2) == "1234567.89")
    assert(f.format_commas(2) == "1,234,567.89")
    assert(f.format_float_commas(2) == "1,234,567.89")
    assert((0.8526).format_percent(1) == "85.3%")
    assert((1234.5).format_currency("\$", 2) == "\$1,234.50")
    assert(f.format(2) == "1234567.89")
    assert(f.to_string() != "")

    let i = 1234567
    assert(i.format_commas() == "1,234,567")
    assert(i.format_int_commas() == "1,234,567")
    assert((1048576).format_bytes() == "1.00 MB")
    assert(i.to_string() == "1234567")
    assert((255).to_hex(true) == "FF")
    assert((255).to_bin() == "11111111")
    assert((255).to_oct() == "377")
    assert(true.to_string() == "true")
    assert(false.to_string() == "false")

    println("All strconv and strfmt tests passed successfully!")
}
