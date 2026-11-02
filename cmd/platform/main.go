// Command platform là CLI vận hành nền tảng. Bước 0 có nhóm lệnh db; pack/push/dev/release thêm ở các bước sau.
//
//	platform db new <dir> <slug>
//	platform db lint [--profile module|platform|master] [--local t1,t2] [<dir>]
//	platform db explain <DBxxx>
//	platform db plan|apply --dsn <url> --target platform|master|module [--dir <dir> --package <key> --schema <s> --version <v> --local t1,t2 --track]
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "db" {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[2] {
	case "new":
		err = dbNew(os.Args[3:])
	case "lint":
		err = dbLint(os.Args[3:])
	case "explain":
		err = dbExplain(os.Args[3:])
	case "plan":
		err = dbPlanOrApply(os.Args[3:], false)
	case "apply":
		err = dbPlanOrApply(os.Args[3:], true)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  platform db new <dir> <slug>
  platform db lint [--profile module|platform|master] [--local t1,t2] [<dir>]
  platform db explain <DBxxx>
  platform db plan|apply --dsn <url> --target platform|master|module [--dir <dir> --package <key> --schema <s> --version <v> --local t1,t2 --track]`)
}
