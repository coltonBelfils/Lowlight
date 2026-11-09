" Vim syntax file
" Language: Lowlight
" Maintainer: Lowlight Team
" Latest Revision: 2026

if exists("b:current_syntax")
  finish
endif

" Comments
syn match   lowlightLineComment      "//.*$" contains=@Spell,lowlightTodo
syn region  lowlightBlockComment     start="/\*" end="\*/" contains=@Spell,lowlightTodo
syn keyword lowlightTodo             contained TODO FIXME XXX NOTE TIP IMPORTANT WARNING CAUTION

" Keywords
syn keyword lowlightKeyword          bind define struct enum interface as import extern
syn keyword lowlightKeyword          let if else match when for in next own
syn keyword lowlightKeyword          unsafe packed test

" Logical Operators
syn keyword lowlightOperatorWord     and or not

" Built-in Types
syn keyword lowlightType             int i8 i16 i32 i64 u8 u16 u32 u64
syn keyword lowlightType             float f32 f64 bool char string
syn keyword lowlightType             Arena ptr ref void never Self

" Built-in Literals
syn keyword lowlightBoolean          true false
syn keyword lowlightConstant         none

" Built-in Functions
syn keyword lowlightBuiltin          print println eprint eprintln assert panic exit parse_int

" Operators & Punctuation
syn match   lowlightOperator         "<-"
syn match   lowlightOperator         "?\."
syn match   lowlightOperator         "??"
syn match   lowlightOperator         "->"
syn match   lowlightOperator         "[-+*%/!=<>&|^~]"

" Number Literals
syn match   lowlightNumber           "\<[0-9][0-9_]*\>"
syn match   lowlightNumber           "\<0x[0-9a-fA-F_]\+\>"
syn match   lowlightNumber           "\<0b[01_]\+\>"
syn match   lowlightNumber           "\<0o[0-7_]\+\>"
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
hi def link lowlightKeyword          Keyword
hi def link lowlightOperatorWord     Operator
hi def link lowlightType             Type
hi def link lowlightBoolean          Boolean
hi def link lowlightConstant         Constant
hi def link lowlightBuiltin          Function
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
