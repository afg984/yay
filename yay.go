package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jguer/yay/aur"
	vcs "github.com/jguer/yay/aur/vcs"
	"github.com/jguer/yay/config"
	pac "github.com/jguer/yay/pacman"
)

func usage() {
	fmt.Println(`usage:  yay <operation> [...]
    operations:
    yay {-h --help}
    yay {-V --version}
    yay {-D --database} <options> <package(s)>
    yay {-F --files}    [options] [package(s)]
    yay {-Q --query}    [options] [package(s)]
    yay {-R --remove}   [options] <package(s)>
    yay {-S --sync}     [options] [package(s)]
    yay {-T --deptest}  [options] [package(s)]
    yay {-U --upgrade}  [options] <file(s)>

    New operations:
    yay -Qstats          displays system information
    yay -Cd              remove unneeded dependencies
    yay -G [package(s)]  get pkgbuild from ABS or AUR

    New options:
    --topdown            shows repository's packages first and then aur's
    --bottomup           shows aur's packages first and then repository's
    --noconfirm          skip user input on package install
	--devel			     Check -git/-svn/-hg development version
	--nodevel			 Disable development version checking
`)
}

var version = "2.116"

func parser() (op string, options []string, packages []string, changedConfig bool, err error) {
	if len(os.Args) < 2 {
		err = fmt.Errorf("no operation specified")
		return
	}
	changedConfig = false
	op = "yogurt"

	for _, arg := range os.Args[1:] {
		if arg[0] == '-' && arg[1] != '-' {
			switch arg {
			default:
				op = arg
			}
			continue
		}

		if arg[0] == '-' && arg[1] == '-' {
			changedConfig = true
			switch arg {
			case "--printconfig":
				fmt.Printf("%+v", config.YayConf)
				os.Exit(0)
			case "--gendb":
				err = aur.CreateDevelDB()
				if err != nil {
					fmt.Println(err)
				}
				err = vcs.SaveBranchInfo()
				if err != nil {
					fmt.Println(err)
				}
				os.Exit(0)
			case "--devel":
				config.YayConf.Devel = true
			case "--nodevel":
				config.YayConf.Devel = false
			case "--timeupdate":
				config.YayConf.TimeUpdate = true
			case "--notimeupdate":
				config.YayConf.TimeUpdate = false
			case "--topdown":
				config.YayConf.SortMode = config.TopDown
			case "--complete":
				config.YayConf.Shell = "sh"
				_ = complete()
				os.Exit(0)
			case "--fcomplete":
				config.YayConf.Shell = "fish"
				_ = complete()
				os.Exit(0)
			case "--help":
				usage()
				os.Exit(0)
			case "--noconfirm":
				config.YayConf.NoConfirm = true
				fallthrough
			default:
				options = append(options, arg)
			}
			continue
		}
		packages = append(packages, arg)
	}
	return
}

func main() {
	op, options, pkgs, changedConfig, err := parser()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	switch op {
	case "-Cd":
		err = cleanDependencies(pkgs)
	case "-G":
		for _, pkg := range pkgs {
			err = getPkgbuild(pkg)
			if err != nil {
				fmt.Println(pkg+":", err)
			}
		}
	case "-Qstats":
		err = localStatistics(version)
	case "-Ss", "-Ssq", "-Sqs":
		if op == "-Ss" {
			config.YayConf.SearchMode = config.Detailed
		} else {
			config.YayConf.SearchMode = config.Minimal
		}

		if pkgs != nil {
			err = syncSearch(pkgs)
		}
	case "-S":
		err = install(pkgs, options)
	case "-Syu", "-Suy":
		err = upgrade(options)
	case "-Si":
		err = syncInfo(pkgs, options)
	case "yogurt":
		config.YayConf.SearchMode = config.NumberMenu

		if pkgs != nil {
			err = numberMenu(pkgs, options)
		}
	default:
		if op[0] == 'R' {
			vcs.RemovePackage(pkgs)
		}
		err = config.PassToPacman(op, pkgs, options)
	}

	var erra error
	if vcs.Updated {
		erra = vcs.SaveBranchInfo()
		if erra != nil {
			fmt.Println(err)
		}

	}

	if changedConfig {
		erra = config.SaveConfig()
		if erra != nil {
			fmt.Println(err)
		}

	}

	erra = config.AlpmHandle.Release()
	if erra != nil {
		fmt.Println(err)
	}

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// NumberMenu presents a CLI for selecting packages to install.
func numberMenu(pkgS []string, flags []string) (err error) {
	var num int

	aq, err := aur.NarrowSearch(pkgS, true)
	if err != nil {
		fmt.Println("Error during AUR search:", err)
	}
	numaq := len(aq)
	pq, numpq, err := pac.Search(pkgS)
	if err != nil {
		return
	}

	if numpq == 0 && numaq == 0 {
		return fmt.Errorf("no packages match search")
	}

	if config.YayConf.SortMode == config.BottomUp {
		printAURSearch(aq, numpq)
		pq.PrintSearch()
	} else {
		pq.PrintSearch()
		printAURSearch(aq, numpq)
	}

	fmt.Printf("\x1b[32m%s\x1b[0m\nNumbers: ", "Type numbers to install. Separate each number with a space.")
	reader := bufio.NewReader(os.Stdin)
	numberBuf, overflow, err := reader.ReadLine()
	if err != nil || overflow {
		fmt.Println(err)
		return
	}

	numberString := string(numberBuf)
	var aurInstall []string
	var repoInstall []string
	result := strings.Fields(numberString)
	for _, numS := range result {
		num, err = strconv.Atoi(numS)
		if err != nil {
			continue
		}

		// Install package
		if num > numaq+numpq-1 || num < 0 {
			continue
		} else if num > numpq-1 {
			if config.YayConf.SortMode == config.BottomUp {
				aurInstall = append(aurInstall, aq[numaq+numpq-num-1].Name)
			} else {
				aurInstall = append(aurInstall, aq[num-numpq].Name)
			}
		} else {
			if config.YayConf.SortMode == config.BottomUp {
				repoInstall = append(repoInstall, pq[numpq-num-1].Name())
			} else {
				repoInstall = append(repoInstall, pq[num].Name())
			}
		}
	}

	if len(repoInstall) != 0 {
		err = config.PassToPacman("-S", repoInstall, flags)
	}

	if len(aurInstall) != 0 {
		err = aur.Install(aurInstall, flags)
	}

	return err
}

// Complete provides completion info for shells
func complete() (err error) {
	path := os.Getenv("HOME") + "/.cache/yay/aur_" + config.YayConf.Shell + ".cache"

	if info, err := os.Stat(path); os.IsNotExist(err) || time.Since(info.ModTime()).Hours() > 48 {
		os.MkdirAll(os.Getenv("HOME")+"/.cache/yay/", 0755)

		out, err := os.Create(path)
		if err != nil {
			return err
		}

		if aur.CreateAURList(out) != nil {
			defer os.Remove(path)
		}
		err = pac.CreatePackageList(out)

		out.Close()
		return err
	}

	in, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return err
	}
	defer in.Close()

	_, err = io.Copy(os.Stdout, in)
	return err
}
