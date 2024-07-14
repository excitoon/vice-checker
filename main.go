package main

import (
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/excitoon/vice-checker/vice"
)

var root = &cobra.Command{
	Use:  "vice-checker URL",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		vice.Check(args[0])
	},
}

var check = &cobra.Command{
	Use:  "check URL",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		vice.Check(args[0])
	},
}

var version = &cobra.Command{
	Use: "version",
	Run: func(cmd *cobra.Command, args []string) {
		println("0.0.0")
	},
}

type Formatter struct {
	log.Formatter
}

func (formatter *Formatter) Format(entry *log.Entry) ([]byte, error) {
	var c *color.Color
	switch entry.Level {
	case log.PanicLevel:
		c = color.New(color.FgHiRed, color.Bold)
	case log.FatalLevel:
		c = color.New(color.FgHiRed, color.Bold)
	case log.ErrorLevel:
		c = color.New(color.FgHiRed, color.Bold)
	case log.WarnLevel:
		c = color.New(color.FgYellow, color.Bold)
	case log.InfoLevel:
		c = color.New(color.FgGreen)
	case log.DebugLevel:
		c = color.New(color.FgCyan)
	default:
		c = color.New()
	}
	t := color.New(color.Faint).SprintFunc()
	m := c.SprintFunc()
	time := fmt.Sprintf("[%s]", entry.Time.Format(time.UnixDate))
	return []byte(fmt.Sprintf("%s %s\n", t(time), m(entry.Message))), nil
}

func main() {
	log.SetFormatter(&Formatter{})
	log.SetLevel(log.TraceLevel)

	root.AddCommand(version)
	root.AddCommand(check)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
