package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// Comprehensive Test Suite for Native Standard Libraries:
// 1. std/chars.ll
// 2. std/strings.ll
// 3. std/fs.ll
// 4. std/os.ll (args)
// 5. std/sort.ll (Sortable, in-place Quicksort, binary search)
// =============================================================================

func TestCharsSuite(t *testing.T) {
	t.Parallel()
	src := `
import "std/chars"

bind main = (): {
    // 1. Character classification methods
    assert(('0').is_digit())
    assert(('9').is_digit())
    assert(not ('a').is_digit())

    assert(('a').is_alpha())
    assert(('Z').is_alpha())
    assert(not ('1').is_alpha())

    assert(('a').is_alnum())
    assert(('5').is_alnum())
    assert(not ('!').is_alnum())

    assert((' ').is_whitespace())
    assert(('\t').is_whitespace())
    assert(('\n').is_whitespace())
    assert(('\r').is_whitespace())
    assert(not ('a').is_whitespace())

    assert(('A').is_ascii_upper())
    assert(not ('a').is_ascii_upper())
    assert(('a').is_ascii_lower())
    assert(not ('A').is_ascii_lower())

    // 2. Case transformation
    assert(('a').to_ascii_upper() == 'A')
    assert(('Z').to_ascii_upper() == 'Z')
    assert(('1').to_ascii_upper() == '1')

    assert(('A').to_ascii_lower() == 'a')
    assert(('z').to_ascii_lower() == 'z')
    assert(('!').to_ascii_lower() == '!')

    // 3. Radix conversions
    assert(('0').to_digit(10) == 0)
    assert(('9').to_digit(10) == 9)
    assert(('f').to_digit(16) == 15)
    assert(('F').to_digit(16) == 15)
    assert(('a').to_digit(10) == none)
    assert(('9').to_digit(8) == none)

    assert(chars.from_digit(0, 10) == '0')
    assert(chars.from_digit(9, 10) == '9')
    assert(chars.from_digit(15, 16) == 'f')
    assert(chars.from_digit(16, 16) == none)

    // 4. UTF-8 length
    assert(('a').len_utf8() == 1)
    assert(('é').len_utf8() == 2)
    assert(('中').len_utf8() == 3)
    assert(('🌍').len_utf8() == 4)

    // 5. Control & punctuation
    assert(('!').is_punctuation())
    assert(('?').is_punctuation())
    assert(not ('a').is_punctuation())
    assert(char(0).is_control())
    assert(char(27).is_control()) // ESC
    assert(not ('A').is_control())

    // 6. Standalone functions
    assert(chars.is_digit('5'))
    assert(chars.is_alpha('x'))
    assert(chars.to_ascii_upper('b') == 'B')
    assert(chars.to_ascii_lower('C') == 'c')
}
`
	runInlineStrict(t, "chars_suite", src, false)
	runInlineStrict(t, "chars_suite_release", src, true)
}

func TestStringsSuite(t *testing.T) {
	t.Parallel()
	src := `
import "std/strings"

bind main = (): {
    // 1. Predicates
    assert("".is_empty())
    assert(not "hello".is_empty())
    assert("   \t\n  ".is_whitespace())
    assert(not "   a   ".is_whitespace())
    assert(not "".is_whitespace())
    assert("ASCII 123".is_ascii())

    // 2. Prefix & suffix
    assert("hello world".starts_with("hello"))
    assert(not "hello world".starts_with("world"))
    assert("hello world".ends_with("world"))
    assert(not "hello world".ends_with("hello"))
    assert("abc".starts_with(""))
    assert("abc".ends_with(""))

    // 3. Search & Count
    let s = "banana banana"
    assert(s.contains("nan"))
    assert(not s.contains("apple"))
    assert(s.index_of("nan") == 2)
    assert(s.index_of("xyz") == none)
    assert(s.last_index_of("nan") == 9)
    assert(s.count("an") == 4)
    assert(s.count("banana") == 2)
    assert(s.count("apple") == 0)

    // 4. Trimming
    assert("   hello   ".trim() == "hello")
    assert("   hello   ".trim_start() == "hello   ")
    assert("   hello   ".trim_end() == "   hello")
    assert("clean".trim() == "clean")

    // 5. Case conversions
    assert("hello WORLD 123!".to_upper() == "HELLO WORLD 123!")
    assert("HELLO world 123!".to_lower() == "hello world 123!")
    assert("lowlight".capitalize() == "Lowlight")
    assert("LOWLIGHT".capitalize() == "Lowlight")

    // 6. Replacement & Manipulation
    let orig = "foo bar foo baz"
    let rep1 = orig.replace("foo", "qux")
    assert(rep1 == "qux bar foo baz")
    let rep_all = orig.replace_all("foo", "qux")
    assert(rep_all == "qux bar qux baz")
    assert("abc".repeat(3) == "abcabcabc")
    assert("abc".repeat(0) == "")
    assert("stressed".reverse() == "desserts")

    // 7. Padding & Alignment
    assert("42".pad_left_char(5, '0') == "00042")
    assert("42".pad_right_char(5, ' ') == "42   ")
    assert("hi".center_char(6, '-') == "--hi--")

    // 8. Splitting & Joining
    let parts = "apple,banana,orange,grape".split(",")
    assert(parts.len() == 4)
    assert(parts[0] == "apple")
    assert(parts[1] == "banana")
    assert(parts[2] == "orange")
    assert(parts[3] == "grape")

    let joined = strings.join(parts, " | ")
    assert(joined == "apple | banana | orange | grape")

    let ws_parts = "  foo   bar   baz  \n qux ".split_whitespace()
    assert(ws_parts.len() == 4)
    assert(ws_parts[0] == "foo")
    assert(ws_parts[1] == "bar")
    assert(ws_parts[2] == "baz")
    assert(ws_parts[3] == "qux")

    let line_list = "line 1\r\nline 2\nline 3".lines()
    assert(line_list.len() == 3)
    assert(line_list[0] == "line 1")
    assert(line_list[1] == "line 2")
    assert(line_list[2] == "line 3")

    // 9. Char & Byte lists
    let chars = "abc".chars_list()
    assert(chars.len() == 3)
    assert(chars[0] == 'a')
    assert(chars[1] == 'b')
    assert(chars[2] == 'c')

    let bytes = "ABC".bytes_list()
    assert(bytes.len() == 3)
    assert(bytes[0] == 65)
    assert(bytes[1] == 66)
    assert(bytes[2] == 67)
}
`
	runInlineStrict(t, "strings_suite", src, false)
	runInlineStrict(t, "strings_suite_release", src, true)
}

func TestFsSuite(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	testFilePath := filepath.Join(tempDir, "test_file.txt")
	testCopyPath := filepath.Join(tempDir, "test_copy.txt")
	testRenamedPath := filepath.Join(tempDir, "test_renamed.txt")

	// Escape path for Lowlight string literal on Windows/Unix
	p1 := strings.ReplaceAll(testFilePath, `\`, `\\`)
	p2 := strings.ReplaceAll(testCopyPath, `\`, `\\`)
	p3 := strings.ReplaceAll(testRenamedPath, `\`, `\\`)

	src := `
import "std/fs"

bind main = (): {
    let path = "` + p1 + `"
    let copy_p = "` + p2 + `"
    let renamed_p = "` + p3 + `"

    // 1. Initial state: does not exist
    assert(not fs.exists(path))

    // 2. Write file
    let w_res! = fs.write_file(path, "Hello Lowlight File System!\n")
    assert(w_res!.is_ok())
    assert(fs.exists(path))

    // 3. Read file
    let r_res! = fs.read_file(path)
    assert(r_res!.is_ok())
    assert(r_res!.unwrap() == "Hello Lowlight File System!\n")

    // 4. Append to file
    let app_res! = fs.append_file(path, "Appended line.\n")
    assert(app_res!.is_ok())
    let r_app! = fs.read_to_string(path)
    assert(r_app!.is_ok())
    assert(r_app!.unwrap() == "Hello Lowlight File System!\nAppended line.\n")

    // 5. File size
    let sz_res! = fs.file_size(path)
    assert(sz_res!.is_ok())
    assert(sz_res!.unwrap() == 43)

    // 6. Robust File stream object (seek, tell, read)
    let f_res! = fs.File.open_read(path)
    assert(f_res!.is_ok())
    let f = f_res!.unwrap()
    assert(f.is_valid())

    // Tell initial position
    assert(f.tell().unwrap() == 0)

    // Seek forward 6 bytes (to "Lowlight...")
    let seek_res! = f.seek(6, fs.SEEK_SET)
    assert(seek_res!.is_ok())
    assert(f.tell().unwrap() == 6)

    // Read remaining from offset 6
    let rem_res! = f.read_to_string()
    assert(rem_res!.is_ok())
    assert(rem_res!.unwrap().starts_with("Lowlight File System!"))

    // Close file
    assert(f.close())
    assert(not f.is_valid())

    // 7. Binary byte reading and writing
    let bin_path = path + ".bin"
    let bytes_to_write: [u8] = [10, 20, 30, 40, 50]
    let wb_res! = fs.write_bytes(bin_path, bytes_to_write)
    assert(wb_res!.is_ok())

    let rb_res! = fs.read_bytes(bin_path)
    assert(rb_res!.is_ok())
    let read_back = rb_res!.unwrap()
    assert(read_back.len() == 5)
    assert(read_back[0] == 10)
    assert(read_back[4] == 50)
    assert(fs.remove_file(bin_path))

    // 8. Copy file
    let cp_res! = fs.copy_file(path, copy_p)
    assert(cp_res!.is_ok())
    assert(cp_res!.unwrap() == 43)
    assert(fs.exists(copy_p))
    assert(fs.read_file(copy_p).unwrap() == fs.read_file(path).unwrap())

    // 9. Rename file
    assert(fs.rename_file(copy_p, renamed_p))
    assert(not fs.exists(copy_p))
    assert(fs.exists(renamed_p))

    // 10. Delete files
    assert(fs.remove_file(path))
    assert(not fs.exists(path))
    assert(fs.remove_file(renamed_p))
    assert(not fs.exists(renamed_p))
}
`
	runInlineStrict(t, "fs_suite", src, false)
	runInlineStrict(t, "fs_suite_release", src, true)
}

func TestOsArgsNative(t *testing.T) {
	t.Parallel()
	src := `
import "std/os"

bind main = (): {
    let cli_args = args()
    // The first argument is always the binary executable path
    assert(cli_args.len() >= 1)
    assert(cli_args[0].len() > 0)
}
`
	runInlineStrict(t, "os_args_native", src, false)
	runInlineStrict(t, "os_args_native_release", src, true)
}

func TestSortSuite(t *testing.T) {
	t.Parallel()
	src := `
import "std/sort"

define Score struct [
    pts: int,
]

bind Score.copy = (self) Score: {
    Score[pts: self.pts]
}

bind Score.cmp = (self, other: Score) int: {
    if (self.pts < other.pts): -1
    else if (self.pts > other.pts): 1
    else: 0
}

bind main = (): {
    // 1. Primitive integer list sorting
    let nums = List[int].new()
    nums.push(5)
    nums.push(2)
    nums.push(8)
    nums.push(1)
    nums.push(9)
    nums.push(3)
    nums.push(7)
    nums.push(4)
    nums.push(6)

    assert(not nums.is_sorted())
    nums.sort()
    assert(nums.is_sorted())
    assert(nums.len == 9)
    assert((nums.get(0) ?? 0) == 1)
    assert((nums.get(1) ?? 0) == 2)
    assert((nums.get(2) ?? 0) == 3)
    assert((nums.get(3) ?? 0) == 4)
    assert((nums.get(4) ?? 0) == 5)
    assert((nums.get(5) ?? 0) == 6)
    assert((nums.get(6) ?? 0) == 7)
    assert((nums.get(7) ?? 0) == 8)
    assert((nums.get(8) ?? 0) == 9)

    // 2. Binary search on sorted list
    assert(nums.binary_search(1) == 0)
    assert(nums.binary_search(5) == 4)
    assert(nums.binary_search(9) == 8)
    assert(nums.binary_search(10) == none)
    assert(nums.binary_search(0) == none)

    // 3. Already sorted and reverse sorted
    let sorted_already = List[int].new()
    sorted_already.push(10)
    sorted_already.push(20)
    sorted_already.push(30)
    sorted_already.sort()
    assert((sorted_already.get(0) ?? 0) == 10 and (sorted_already.get(2) ?? 0) == 30)

    let rev = List[int].new()
    rev.push(30)
    rev.push(20)
    rev.push(10)
    rev.sort()
    assert((rev.get(0) ?? 0) == 10 and (rev.get(1) ?? 0) == 20 and (rev.get(2) ?? 0) == 30)

    // 4. Edge cases: empty and single element
    let empty_list = List[int].new()
    empty_list.sort()
    assert(empty_list.len == 0)
    assert(empty_list.is_sorted())

    let single = List[int].new()
    single.push(42)
    single.sort()
    assert(single.len == 1 and (single.get(0) ?? 0) == 42)
    assert(single.is_sorted())

    // 5. Duplicates
    let dups = List[int].new()
    dups.push(3)
    dups.push(1)
    dups.push(4)
    dups.push(1)
    dups.push(5)
    dups.push(9)
    dups.push(2)
    dups.push(6)
    dups.push(5)
    dups.push(3)
    dups.push(5)
    dups.sort()
    assert(dups.is_sorted())
    assert((dups.get(0) ?? 0) == 1 and (dups.get(1) ?? 0) == 1 and (dups.get(dups.len - 1) ?? 0) == 9)

    // 6. String list sorting (lexicographical)
    let words = List[string].new()
    words.push("banana")
    words.push("apple")
    words.push("date")
    words.push("cherry")
    words.sort()
    assert(words.is_sorted())
    assert((words.get(0) ?? "") == "apple")
    assert((words.get(1) ?? "") == "banana")
    assert((words.get(2) ?? "") == "cherry")
    assert((words.get(3) ?? "") == "date")

    // 7. Custom Comparable struct sorting
    let scores = List[Score].new()
    scores.push(Score[pts: 300])
    scores.push(Score[pts: 100])
    scores.push(Score[pts: 500])
    scores.push(Score[pts: 200])
    scores.sort()
    assert((scores.get(0) ?? Score[pts: 0]).pts == 100)
    assert((scores.get(1) ?? Score[pts: 0]).pts == 200)
    assert((scores.get(2) ?? Score[pts: 0]).pts == 300)
    assert((scores.get(3) ?? Score[pts: 0]).pts == 500)

    // 8. Custom comparator sorting (descending sort_by)
    let desc_nums = List[int].new()
    desc_nums.push(10)
    desc_nums.push(50)
    desc_nums.push(20)
    desc_nums.push(40)
    desc_nums.push(30)
    let desc_cmp = (a: int, b: int) int: {
        if (a > b): -1
        else if (a < b): 1
        else: 0
    }
    desc_nums.sort_by(desc_cmp)
    assert((desc_nums.get(0) ?? 0) == 50)
    assert((desc_nums.get(1) ?? 0) == 40)
    assert((desc_nums.get(2) ?? 0) == 30)
    assert((desc_nums.get(3) ?? 0) == 20)
    assert((desc_nums.get(4) ?? 0) == 10)

    // 9. In-place reverse
    let to_rev = List[int].new()
    to_rev.push(1)
    to_rev.push(2)
    to_rev.push(3)
    to_rev.push(4)
    to_rev.push(5)
    to_rev.reverse()
    assert((to_rev.get(0) ?? 0) == 5 and (to_rev.get(4) ?? 0) == 1)

    // 10. Standalone helper functions & stable_sort / bounds / [N: T]
    let helper_list = List[int].new()
    helper_list.push(9)
    helper_list.push(1)
    helper_list.push(5)
    helper_list.push(5)
    sort.stable_sort(&helper_list)
    assert(sort.is_sorted(helper_list))
    assert(sort.binary_search(helper_list, 5) != none)
    assert(sort.binary_search_by(helper_list, (x: int) int: x - 9) == 3)
    assert(sort.lower_bound(helper_list, 5) == 1)
    assert(sort.upper_bound(helper_list, 5) == 3)

    let fixed_arr = [4: 40, 10, 30, 20]
    fixed_arr.sort()
    assert(fixed_arr.is_sorted())
    assert(sort.lower_bound(fixed_arr, 25) == 2)
    assert(sort.upper_bound(fixed_arr, 30) == 3)

    // 11. Extension on Set[T]
    let set = Set[int].new()
    set.insert(30)
    set.insert(10)
    set.insert(20)
    let sorted_set = set.to_sorted_list()
    assert(sorted_set.is_sorted())
    assert(sorted_set.len == 3)
    assert((sorted_set.get(0) ?? 0) == 10)
    assert((sorted_set.get(1) ?? 0) == 20)
    assert((sorted_set.get(2) ?? 0) == 30)

    // 12. Extension on Map[K, V]
    let map = Map[string, int].new()
    map.insert("charlie", 3)
    map.insert("alice", 1)
    map.insert("bob", 2)
    let sorted_keys = map.to_sorted_keys()
    assert(sorted_keys.is_sorted())
    assert(sorted_keys.len == 3)
    assert((sorted_keys.get(0) ?? "") == "alice")
    assert((sorted_keys.get(1) ?? "") == "bob")
    assert((sorted_keys.get(2) ?? "") == "charlie")
}
`
	runInlineStrict(t, "sort_suite", src, false)
	runInlineStrict(t, "sort_suite_release", src, true)
}
