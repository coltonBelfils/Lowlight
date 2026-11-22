" Vim syntax file
" Language: Lowlight
" Maintainer: Lowlight Team
" Latest Revision: 2026

if exists("b:current_syntax")
  finish
endif

" Comments & Documentation
syn match   lowlightLineComment      "//.*$" contains=@Spell,lowlightTodo,lowlightDocHeading,lowlightDocLink
syn region  lowlightBlockComment     start="/\*" end="\*/" contains=@Spell,lowlightTodo,lowlightDocHeading,lowlightDocLink
syn keyword lowlightTodo             contained TODO FIXME XXX NOTE TIP IMPORTANT WARNING CAUTION
syn match   lowlightDocHeading       contained "#\s\+\(Arguments\|Returns\|Errors\|Panics\|Examples\|Safety\|Complexity\|Notes\?\)\>"
syn match   lowlightDocLink          contained "\[`[^`]\+`\]"

" Keywords
syn keyword lowlightKeyword          bind define distinct struct enum interface as import extern
syn keyword lowlightKeyword          let if else match when for while in next own unpack
syn keyword lowlightKeyword          unsafe packed test func fn cl
syn keyword lowlightSelf             self
" Contextual keywords: `define Name result [...]`, a match / result chain's `default` arm,
" and its `default panic` terminal arm.
syn match   lowlightKeyword          "\(\<define\s\+\h\w*\(\[[^]]*\]\)\?\s\+\)\@<=result\>"
syn match   lowlightKeyword          "^\s*\zsdefault\>\ze\s*\(:\|\h\)"
syn match   lowlightKeyword          "\<default\s\+\zspanic\>\ze\s*\((\)\@!"

" Result identifiers (`cfg!`) and optional identifiers (`user?`)
syn match   lowlightResultIdent      "\<\h\w*!\(=\)\@!"
syn match   lowlightOptionalIdent    "\<\h\w*?\([?.]\)\@!"

" Attributes
syn match   lowlightAttribute        "@[A-Za-z_][A-Za-z0-9_]*"

" Logical Operators
syn keyword lowlightOperatorWord     and or not

" Built-in Types
syn keyword lowlightType             int i8 i16 i32 i64 u8 u16 u32 u64
syn keyword lowlightType             float f32 f64 bool char string
syn keyword lowlightType             Arena ptr ref void never Self

" Built-in Literals
syn keyword lowlightBoolean          true false
syn keyword lowlightConstant         none

" Built-in Functions & Intrinsics
syn keyword lowlightBuiltin          print println eprint eprintln assert panic exit parse_int parse_float
syn keyword lowlightBuiltin          sqrt abs min max read_file write_file args
syn keyword lowlightBuiltin          caller_location caller_file caller_line caller_col
syn keyword lowlightIntrinsic        size_of align_of alloc_raw realloc_raw free_raw null_ptr is_null
syn keyword lowlightIntrinsic        addr_of ptr_add ptr_read ptr_write ptr_cast ptr_addr copy_bytes zero_bytes

" Operators & Punctuation
syn match   lowlightOperator         "[+#]\["
syn match   lowlightOperator         "<-"
syn match   lowlightOperator         "?\."
syn match   lowlightOperator         "??"
syn match   lowlightOperator         "->"
syn match   lowlightOperator         "[-+*%/!=<>&|^~]"

" Number Literals
syn match   lowlightNumber           "\<[0-9][0-9_]*\>"
syn match   lowlightNumber           "\<0x[0-9a-fA-F_]\+\>"
syn match   lowlightNumber           "\<0b[01_]\+\>"
syn match   lowlightFloat            "\<[0-9][0-9_]*\.[0-9][0-9_]*\([eE][+-]\?[0-9_]\+\)\?\>"

" Character Literals
syn match   lowlightCharacter        "'[^'\\]'"
syn match   lowlightCharacter        "'\\\(x[0-9a-fA-F]\{2}\|u{[0-9a-fA-F]\+}\|[nrt0\\'\"]\)'"

" String Literals & Interpolation
syn region  lowlightString           start=+"+ skip=+\\"+ end=+"+ contains=lowlightEscape,lowlightInterpolation
syn region  lowlightBacktickString   start=+`+ skip=+\\`+ end=+`+ contains=lowlightEscape,lowlightInterpolation
syn match   lowlightEscape           contained "\\\(x[0-9a-fA-F]\{2}\|u{[0-9a-fA-F]\+}\|[nrt0\\'"\`$]\)"
syn match   lowlightInterpolation    contained "\$[a-zA-Z_][a-zA-Z0-9_]*"
syn region  lowlightInterpolation    contained matchgroup=lowlightInterpDelim start="\${" end="}" contains=TOP

hi def link lowlightLineComment      Comment
hi def link lowlightBlockComment     Comment
hi def link lowlightTodo             Todo
hi def link lowlightDocHeading       Title
hi def link lowlightDocLink          Underlined
hi def link lowlightKeyword          Keyword
hi def link lowlightSelf             Keyword
hi def link lowlightResultIdent      Identifier
hi def link lowlightOptionalIdent    Identifier
hi def link lowlightAttribute        PreProc
hi def link lowlightOperatorWord     Operator
hi def link lowlightType             Type
hi def link lowlightBoolean          Boolean
hi def link lowlightConstant         Constant
hi def link lowlightBuiltin          Function
hi def link lowlightIntrinsic        Function
hi def link lowlightOperator         Operator
hi def link lowlightNumber           Number
hi def link lowlightFloat            Float
hi def link lowlightCharacter        Character
hi def link lowlightString           String
hi def link lowlightBacktickString   String
hi def link lowlightEscape           SpecialChar
hi def link lowlightInterpolation    Special
hi def link lowlightInterpDelim      Delimiter

let b:current_syntax = "lowlight"
