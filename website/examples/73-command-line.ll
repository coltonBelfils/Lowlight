// Title: Command-line programs
// Category: Standard library
// Order: 73

// `args()` returns the program name followed by its arguments.
// Run with arguments: lowlight run app.ll -- --name Ada
bind main = fn() int: {
	let argv = args()
	let name = "world"
	let i = 1
	while(i < argv.len()): {
		if(argv[i] == "--name" and i + 1 < argv.len()): {
			name = argv[i + 1].copy()
			i += 1
		}
		i += 1
	}
	println("hello, $name")

	// Diagnostics go to standard error.
	if(argv.len() > 10): {
		eprintln("too many arguments")
		func <- 2
	}

	// `main` may return the process's exit status.
	0
}
