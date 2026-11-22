// examples/ownership_and_borrows.ll
// Showcases Model 2A value ownership:
// - Plain types copy implicitly; Resource types move with `own` or clone via `.copy()`
// - Read-only borrows (zero heap copies) vs `&` mutable borrows
// - Custom deterministic `Drop` (`bind Tracker.drop = fn(&self)`)
// - Recursive value-owned trees via auto-boxed optional `?TreeNode`

define Tracker struct [
    label: string,
    events: [string],
]

bind Tracker.new = fn(label: string) Tracker: {
    Tracker[
        label: label.copy(),
        events: ["created:$label"],
    ]
}

bind Tracker.record = fn(&self, event: string): {
    self.events.push(event.copy())
}

bind Tracker.summary = fn(self) string: {
    "${self.label}(${self.events.join(", ")})"
}

bind Tracker.drop = fn(&self): {
    println("  [drop] closing Tracker '${self.label}' with ${self.events.len()} event(s)")
}

// Recursive binary search tree: `?BstNode` is automatically heap-boxed and
// deterministically freed without manual pointers or reference cycles!
define BstNode struct [
    key: int,
    tag: string,
    left?: ?BstNode,
    right?: ?BstNode,
]

bind BstNode.leaf = fn(key: int, tag: string) BstNode: {
    BstNode[
        key: key,
        tag: tag.copy(),
        left?: none,
        right?: none,
    ]
}

bind BstNode.insert = fn(&self, key: int, tag: string) void: {
    if (key < self.key): {
        if (&self.left? as left_child): {
            left_child.insert(key, tag)
        } else: {
            self.left? = BstNode.leaf(key, tag)
        }
    } else if (key > self.key): {
        if (&self.right? as right_child): {
            right_child.insert(key, tag)
        } else: {
            self.right? = BstNode.leaf(key, tag)
        }
    } else: {
        self.tag = tag.copy()
    }
}

bind BstNode.find = fn(self, key: int) ?string: {
    if (key == self.key): {
        func <- self.tag.copy()
    }
    if (key < self.key): {
        if (self.left? as l): {
            func <- l.find(key)
        }
        func <- none
    }
    if (self.right? as r): {
        func <- r.find(key)
    }
    func <- none
}

bind consume_names = fn(names: own [string]) string: {
    names.join(" -> ")
}

bind append_greeting = fn(buf: &string, who: string): {
    buf.push("Hello, ")
    buf.push(who)
    buf.push("!")
}

bind main = fn(): {
    // 1. Mutable borrow `&` vs read-only borrow
    let msg = ""
    append_greeting(&msg, "Lowlight")
    assert(msg == "Hello, Lowlight!")

    // 2. Explicit `own` transfer vs `.copy()`
    let pipeline = ["lex", "parse", "sema", "mir", "c11"]
    let snapshot = pipeline.copy()
    let joined = consume_names(own pipeline)
    assert(joined == "lex -> parse -> sema -> mir -> c11")
    assert(snapshot.len() == 5)

    // 3. Deterministic LIFO custom Drop
    {
        let t1 = Tracker.new("alpha")
        let t2 = Tracker.new("beta")
        t1.record("step1")
        t2.record("stepA")
        t2.record("stepB")
        println("Active trackers: ${t1.summary()} | ${t2.summary()}")
    }

    // 4. Recursive Binary Search Tree
    let root = BstNode.leaf(50, "root")
    root.insert(25, "left")
    root.insert(75, "right")
    root.insert(10, "far-left")
    root.insert(60, "mid-right")

    assert((root.find(60) ?? "missing") == "mid-right")
    assert((root.find(10) ?? "missing") == "far-left")
    assert(root.find(99) == none)

    println("ownership_and_borrows: tree lookup 60=${root.find(60) ?? "?"}")
}
