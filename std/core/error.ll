// =============================================================================
// Lowlight Core Error & ErrorFrame Types (Go-style Native Error Chain)
// =============================================================================

import "./interfaces.ll"
import "./list.ll"

define ErrorFrame struct [
    _file: string,
    _line: int,
    _col: int,
    _note?: ?string,
]

bind ErrorFrame.new = (file: string, line: int, col: int, note?: ?string) ErrorFrame: {
    ErrorFrame[
        _file: file.copy(),
        _line: line,
        _col: col,
        _note?: if (note? as n): n.copy() else: none,
    ]
}

bind ErrorFrame.file = (self) string: {
    self._file.copy()
}

bind ErrorFrame.line = (self) int: {
    self._line
}

bind ErrorFrame.col = (self) int: {
    self._col
}

bind ErrorFrame.note = (self) ?string: {
    if (self._note? as n): n.copy() else: none
}

bind ErrorFrame.location = (self) string: {
    "${self._file}:${self._line}:${self._col}"
}

bind ErrorFrame.to_string = (self) string: {
    if (self._note? as n): {
        "${self._file}:${self._line}:${self._col} — $n"
    } else: {
        "${self._file}:${self._line}:${self._col}"
    }
}

define Error struct [
    _message: string,
    _code?: ?string,
    _cause?: ?Error,
    _frames: [ErrorFrame],
]

@track_caller
bind Error.new = (msg: string) Error: {
    let f = ErrorFrame[
        _file: caller_file(),
        _line: caller_line(),
        _col: caller_col(),
        _note?: msg.copy(),
    ]
    Error[
        _message: msg.copy(),
        _code?: none,
        _cause?: none,
        _frames: [own f],
    ]
}

@track_caller
bind Error.coded = (code: string, msg: string) Error: {
    let f = ErrorFrame[
        _file: caller_file(),
        _line: caller_line(),
        _col: caller_col(),
        _note?: msg.copy(),
    ]
    Error[
        _message: msg.copy(),
        _code?: code.copy(),
        _cause?: none,
        _frames: [own f],
    ]
}

@track_caller
bind Error.wrap = (self, msg: string) Error: {
    let f = ErrorFrame[
        _file: caller_file(),
        _line: caller_line(),
        _col: caller_col(),
        _note?: msg.copy(),
    ]
    Error[
        _message: msg.copy(),
        _code?: none,
        _cause?: self.copy(),
        _frames: [own f],
    ]
}

@track_caller
bind Error.trace = (self) Error: {
    let cp = self.copy()
    cp._frames.push(ErrorFrame[
        _file: caller_file(),
        _line: caller_line(),
        _col: caller_col(),
        _note?: none,
    ])
    cp
}

bind Error.with_code = (self, code: string) Error: {
    let cp = self.copy()
    cp._code? = code.copy()
    cp
}

bind Error.message = (self) string: {
    self._message.copy()
}

bind Error.code = (self) ?string: {
    if (self._code? as c): func <- c.copy()
    if (self._cause? as cause): func <- cause.code()
    none
}

bind Error.cause = (self) ?Error: {
    if (self._cause? as c): c.copy() else: none
}

bind Error.root = (self) Error: {
    if (self._cause? as c): c.root() else: self.copy()
}

bind Error.frames = (self) [ErrorFrame]: {
    self._frames.copy()
}

bind Error.is = (self, target: Error) bool: {
    if (target._code? as tcode): {
        if (self._code? as ccode): {
            if (ccode == tcode): func <- true
        }
    } else if (self._code? == none and self._message == target._message): {
        func <- true
    }
    if (self._cause? as cause): {
        func <- cause.is(target)
    }
    false
}

bind Error.has_code = (self, code: string) bool: {
    if (self._code? as c): {
        if (c == code): func <- true
    }
    if (self._cause? as cause): {
        func <- cause.has_code(code)
    }
    false
}

bind Error.summary = (self) string: {
    let out = self._message.copy()
    if (self._cause? as c): {
        out.push(": ")
        out.push(c.summary())
    }
    out
}

bind Error._append_trace = (self, out: &string) void: {
    let i = self._frames.len() - 1
    for (i >= 0): {
        out.push("\n  at ")
        out.push(self._frames[i].to_string())
        i -= 1
    }
    if (self._cause? as c): {
        c._append_trace(&out)
    }
}

bind Error.to_string = (self) string: {
    let out = ""
    if (self.code() as c): {
        out.push("[")
        out.push(c)
        out.push("] ")
    }
    out.push(self.summary())
    out.push("\ntrace:")
    self._append_trace(&out)
    out
}

bind Error.eq = (self, other: Error) bool: {
    if (self._message != other._message): func <- false
    if (self._code? != other._code?): func <- false
    if (self._cause? != other._cause?): func <- false
    if (self._frames.len() != other._frames.len()): func <- false
    for (i in 0..self._frames.len()): {
        if (self._frames[i] != other._frames[i]): func <- false
    }
    true
}
