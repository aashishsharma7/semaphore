package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/ansible-semaphore/semaphore/api"
	"github.com/ansible-semaphore/semaphore/api/sockets"
	"github.com/ansible-semaphore/semaphore/api/tasks"
	"github.com/ansible-semaphore/semaphore/db"
	"github.com/ansible-semaphore/semaphore/util"
	"github.com/gorilla/handlers"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	util.ConfigInit()
	if util.InteractiveSetup {
		os.Exit(doSetup())
	}

	if util.Upgrade {
		if err := util.DoUpgrade(util.Version); err != nil {
			panic(err)
		}

		os.Exit(0)
	}

	fmt.Printf("Semaphore %v\n", util.Version)
	fmt.Printf("Interface %v\n", util.Config.Interface)
	fmt.Printf("Port %v\n", util.Config.Port)
	fmt.Printf("MySQL %v@%v %v\n", util.Config.MySQL.Username, util.Config.MySQL.Hostname, util.Config.MySQL.DbName)
	fmt.Printf("Tmp Path (projects home) %v\n", util.Config.TmpPath)

	if err := db.Connect(); err != nil {
		fmt.Println("\n Have you run semaphore -setup?")
		panic(err)
	}

	db.SetupDBLink()
	defer db.Close()

	if err := db.MigrateAll(); err != nil {
		panic(err)
	}
	// legacy
	if util.Migration {
		fmt.Println("\n DB migrations run on startup automatically")
		return
	}

	go sockets.StartWS()
	go checkUpdates()
	go tasks.StartRunner()

	var router http.Handler = api.Route()
	router = handlers.ProxyHeaders(router)
	http.Handle("/", router)
	err := http.ListenAndServe(util.Config.Interface+util.Config.Port, nil)
	if err != nil {
		log.Panic(err)
	}
}

//nolint: gocyclo
func doSetup() int {
	fmt.Print(`
 Hello! You will now be guided through a setup to:

 1. Set up configuration for a MySQL/MariaDB database
 2. Set up a path for your playbooks (auto-created)
 3. Run database Migrations
 4. Set up initial semaphore user & password

`)

	var b []byte
	setup := util.NewConfig()
	for {
		setup.Scan()
		setup.GenerateCookieSecrets()

		var err error
		b, err = json.MarshalIndent(&setup, " ", "\t")
		if err != nil {
			panic(err)
		}

		fmt.Printf("\n Generated configuration:\n %v\n\n", string(b))
		fmt.Print(" > Is this correct? (yes/no): ")

		var answer string
		util.ScanErrorChecker(fmt.Scanln(&answer))
		if answer == "yes" || answer == "y" {
			break
		}

		fmt.Println()
		setup = util.NewConfig()
	}

	confDir, err := os.Getwd()
	if err != nil {
		confDir = "/etc/semaphore"
	}
	fmt.Print(" > Config output directory (default " + confDir + "): ")

	var answer string
	util.ScanErrorChecker(fmt.Scanln(&answer))
	if len(answer) > 0 {
		confDir = answer
	}

	fmt.Printf(" Running: mkdir -p %v..\n", confDir)
	err = os.MkdirAll(confDir, 0755) //nolint: gas
	if err != nil {
		log.Panic("Could not create config directory: " + err.Error())
	}

	configPath := path.Join(confDir, "/config.json")
	if err = ioutil.WriteFile(configPath, b, 0644); err != nil {
		panic(err)
	}
	fmt.Printf(" Configuration written to %v..\n", configPath)

	fmt.Println(" Pinging db..")
	util.Config = setup

	if err = db.Connect(); err != nil {
		fmt.Printf("\n Cannot connect to database!\n %v\n", err.Error())
		os.Exit(1)
	}

	fmt.Println("\n Running DB Migrations..")
	if err = db.MigrateAll(); err != nil {
		fmt.Printf("\n Database migrations failed!\n %v\n", err.Error())
		os.Exit(1)
	}

	stdin := bufio.NewReader(os.Stdin)

	var user db.User
	user.Username = readNewline("\n\n > Username: ", stdin)
	user.Username = strings.ToLower(user.Username)
	user.Email = readNewline(" > Email: ", stdin)
	user.Email = strings.ToLower(user.Email)

	var existingUser db.User
	err = db.Mysql.SelectOne(&existingUser, "select * from user where email=? or username=?", user.Email, user.Username)
	util.LogWarning(err)

	if existingUser.ID > 0 {
		// user already exists
		fmt.Printf("\n Welcome back, %v! (a user with this username/email is already set up..)\n\n", existingUser.Name)
	} else {
		user.Name = readNewline(" > Your name: ", stdin)
		user.Password = readNewline(" > Password: ", stdin)
		pwdHash, err := bcrypt.GenerateFromPassword([]byte(user.Password), 11)
		util.LogWarning(err)

		if _, err := db.Mysql.Exec("insert into user set name=?, username=?, email=?, password=?, admin=1, created=UTC_TIMESTAMP()", user.Name, user.Username, user.Email, pwdHash); err != nil {
			fmt.Printf(" Inserting user failed. If you already have a user, you can disregard this error.\n %v\n", err.Error())
			os.Exit(1)
		}

		fmt.Printf("\n You are all setup %v!\n", user.Name)
	}

	fmt.Printf(" Re-launch this program pointing to the configuration file\n\n./semaphore -config %v\n\n", configPath)
	fmt.Printf(" To run as daemon:\n\nnohup ./semaphore -config %v &\n\n", configPath)
	fmt.Printf(" You can login with %v or %v.\n", user.Email, user.Username)

	return 0
}

func readNewline(pre string, stdin *bufio.Reader) string {
	fmt.Print(pre)

	str, err := stdin.ReadString('\n')
	util.LogWarning(err)
	str = strings.Replace(strings.Replace(str, "\n", "", -1), "\r", "", -1)

	return str
}

// checkUpdates is a goroutine that periodically checks for application updates
// does not exit on errors.
func checkUpdates() {
	handleUpdateError(util.CheckUpdate(util.Version))

	t := time.NewTicker(time.Hour * 24)

	for range t.C {
		handleUpdateError(util.CheckUpdate(util.Version))
	}
}

func handleUpdateError(err error) {
	if err != nil {
		log.Warn("Could not check for update: " + err.Error())
	}
}
