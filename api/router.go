package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ansible-semaphore/semaphore/api/projects"
	"github.com/ansible-semaphore/semaphore/api/sockets"
	"github.com/ansible-semaphore/semaphore/api/tasks"

	"github.com/ansible-semaphore/semaphore/util"
	"github.com/gobuffalo/packr"
	"github.com/gorilla/mux"
	"github.com/russross/blackfriday"
)

var publicAssets = packr.NewBox("../web/public")

//JSONMiddleware ensures that all the routes respond with Json, this is added by default to all routes
func JSONMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		next.ServeHTTP(w, r)
	})
}

//plainTextMiddleware resets headers to Plain Text if needed
func plainTextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		next.ServeHTTP(w, r)
	})
}

func pongHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("pong"))
}

func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte("404 not found"))
	fmt.Println(r.Method, ":", r.URL.String(), "--> 404 Not Found")
}

// Route declares all routes
func Route() *mux.Router {
	r := mux.NewRouter()
	r.NotFoundHandler = http.HandlerFunc(servePublic)

	webPath := "/"
	if util.WebHostURL != nil {
		webPath = util.WebHostURL.RequestURI()
	}

	r.HandleFunc(webPath, http.HandlerFunc(servePublic))
	r.Use(mux.CORSMethodMiddleware(r), JSONMiddleware)

	r.HandleFunc("/api/auth/login", login).Methods("POST")
	r.HandleFunc("/api/auth/logout", logout).Methods("POST")
	r.HandleFunc("/api/ping", pongHandler).Methods("GET", "HEAD").Subrouter().Use(plainTextMiddleware)

	// set up the namespace
	api := r.PathPrefix(webPath + "api").Subrouter()

	api.Use(authentication)

	api.HandleFunc("/ws", sockets.Handler).Methods("GET", "HEAD")
	api.HandleFunc("/info", getSystemInfo).Methods("GET", "HEAD")
	api.HandleFunc("/upgrade", checkUpgrade).Methods("GET", "HEAD")
	api.HandleFunc("/upgrade", doUpgrade).Methods("POST")

	user := api.PathPrefix("/user").Subrouter()

	user.HandleFunc("", getUser).Methods("GET", "HEAD")
	user.HandleFunc("/tokens", getAPITokens).Methods("GET", "HEAD")
	user.HandleFunc("/tokens", createAPIToken).Methods("POST")
	user.HandleFunc("/tokens/{token_id}", expireAPIToken).Methods("DELETE")

	api.HandleFunc("/projects", projects.GetProjects).Methods("GET", "HEAD")
	api.HandleFunc("/projects", projects.AddProject).Methods("POST")
	api.HandleFunc("/events", getAllEvents).Methods("GET", "HEAD")
	api.HandleFunc("/events/last", getLastEvents).Methods("GET", "HEAD")

	api.HandleFunc("/users", getUsers).Methods("GET", "HEAD")
	api.HandleFunc("/users", addUser).Methods("POST")
	apiUserID := api.PathPrefix("/users/{user_id}").Subrouter()
	apiUserID.Use(getUserMiddleware)
	apiUserID.HandleFunc("", getUser).Methods("GET", "HEAD")
	apiUserID.HandleFunc("", updateUser).Methods("PUT")
	apiUserID.HandleFunc("/password", updateUserPassword).Methods("POST")
	apiUserID.HandleFunc("", deleteUser).Methods("Delete")

	project := api.PathPrefix("/project/{project_id}").Subrouter()

	project.Use(projects.ProjectMiddleware)

	project.HandleFunc("", projects.GetProject).Methods("GET", "HEAD")
	project.HandleFunc("", projects.UpdateProject).Methods("PUT").Subrouter().Use(projects.MustBeAdmin)
	project.HandleFunc("", projects.DeleteProject).Methods("DELETE").Subrouter().Use(projects.MustBeAdmin)

	project.HandleFunc("/events", getAllEvents).Methods("GET", "HEAD")
	project.HandleFunc("/events/last", getLastEvents).Methods("GET", "HEAD")

	project.HandleFunc("/users", projects.GetUsers).Methods("GET", "HEAD")
	project.HandleFunc("/users", projects.AddUser).Methods("POST").Subrouter().Use(projects.MustBeAdmin)
	projectUserID := project.PathPrefix("/users/{user_id}").Subrouter()
	projectUserID.Use(projects.UserMiddleware, projects.MustBeAdmin)
	projectUserID.HandleFunc("/admin", projects.MakeUserAdmin).Methods("POST")
	projectUserID.HandleFunc("/admin", projects.MakeUserAdmin).Methods("DELETE")
	projectUserID.HandleFunc("", projects.RemoveUser).Methods("DELETE")

	project.HandleFunc("/keys", projects.GetKeys).Methods("GET", "HEAD")
	project.HandleFunc("/keys", projects.AddKey).Methods("POST")
	projectKeyID := project.PathPrefix("/keys/{key_id}").Subrouter()
	projectKeyID.Use(projects.KeyMiddleware)
	projectKeyID.HandleFunc("", projects.UpdateKey).Methods("PUT")
	projectKeyID.HandleFunc("", projects.RemoveKey).Methods("DELETE")

	project.HandleFunc("/repositories", projects.GetRepositories).Methods("GET", "HEAD")
	project.HandleFunc("/repositories", projects.AddRepository).Methods("POST")
	projectRepID := project.PathPrefix("/repositories/{repository_id}").Subrouter()
	projectRepID.Use(projects.RepositoryMiddleware)
	projectRepID.HandleFunc("", projects.UpdateRepository).Methods("PUT")
	projectRepID.HandleFunc("", projects.RemoveRepository).Methods("DELETE")

	project.HandleFunc("/inventory", projects.GetInventory).Methods("GET", "HEAD")
	project.HandleFunc("/inventory", projects.AddInventory).Methods("POST")
	projectInvID := project.PathPrefix("/inventory/{inventory_id}").Subrouter()
	projectInvID.Use(projects.InventoryMiddleware)
	projectInvID.HandleFunc("", projects.UpdateInventory).Methods("PUT")
	projectInvID.HandleFunc("", projects.RemoveInventory).Methods("DELETE")

	project.HandleFunc("/environment", projects.GetEnvironment).Methods("GET", "HEAD")
	project.HandleFunc("/environment", projects.AddEnvironment).Methods("POST")
	projectEnvID := project.PathPrefix("/environment/{environment_id}").Subrouter()
	projectEnvID.Use(projects.EnvironmentMiddleware)
	projectEnvID.HandleFunc("", projects.RemoveEnvironment).Methods("DELETE")
	projectEnvID.HandleFunc("", projects.UpdateEnvironment).Methods("PUT")

	project.HandleFunc("/templates", projects.GetTemplates).Methods("GET", "HEAD")
	project.HandleFunc("/templates", projects.AddTemplate).Methods("POST")
	projectTemplID := project.PathPrefix("/templates/{template_id}").Subrouter()
	projectTemplID.Use(projects.TemplatesMiddleware)
	projectTemplID.HandleFunc("", projects.UpdateTemplate).Methods("PUT")
	projectTemplID.HandleFunc("", projects.RemoveTemplate).Methods("DELETE")

	project.HandleFunc("/tasks", tasks.GetAllTasks).Methods("GET", "HEAD")
	project.HandleFunc("/tasks/last", tasks.GetLastTasks).Methods("GET", "HEAD")
	project.HandleFunc("/tasks", tasks.AddTask).Methods("POST")
	projectTaskID := project.PathPrefix("/tasks/{task_id}").Subrouter()
	projectTaskID.Use(tasks.GetTaskMiddleware)
	projectTaskID.HandleFunc("/output", tasks.GetTaskOutput).Methods("GET", "HEAD")
	projectTaskID.HandleFunc("", tasks.GetTask).Methods("GET", "HEAD")
	projectTaskID.HandleFunc("", tasks.RemoveTask).Methods("DELETE")

	return r
}

//nolint: gocyclo
func servePublic(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if strings.HasPrefix(path, "/api") {
		notFoundHandler(w, r)
		return
	}

	webPath := "/"
	if util.WebHostURL != nil {
		webPath = util.WebHostURL.RequestURI()
	}

	if !strings.HasPrefix(path, webPath+"public") {
		if len(strings.Split(path, ".")) > 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		path = "/html/index.html"
	}

	path = strings.Replace(path, webPath+"public/", "", 1)
	split := strings.Split(path, ".")
	suffix := split[len(split)-1]

	res, err := publicAssets.MustBytes(path)
	if err != nil {
		notFoundHandler(w, r)
		return
	}

	// replace base path
	if util.WebHostURL != nil && path == "/html/index.html" {
		res = []byte(strings.Replace(string(res),
			"<base href=\"/\">",
			"<base href=\""+util.WebHostURL.String()+"\">",
			1))
	}

	contentType := "text/plain"
	switch suffix {
	case "png":
		contentType = "image/png"
	case "jpg", "jpeg":
		contentType = "image/jpeg"
	case "gif":
		contentType = "image/gif"
	case "js":
		contentType = "application/javascript"
	case "css":
		contentType = "text/css"
	case "woff":
		contentType = "application/x-font-woff"
	case "ttf":
		contentType = "application/x-font-ttf"
	case "otf":
		contentType = "application/x-font-otf"
	case "html":
		contentType = "text/html"
	}

	w.Header().Set("content-type", contentType)
	_, err = w.Write(res)
	util.LogWarning(err)
}

func getSystemInfo(w http.ResponseWriter, r *http.Request) {
	body := map[string]interface{}{
		"version": util.Version,
		"update":  util.UpdateAvailable,
		"config": map[string]string{
			"dbHost":  util.Config.MySQL.Hostname,
			"dbName":  util.Config.MySQL.DbName,
			"dbUser":  util.Config.MySQL.Username,
			"path":    util.Config.TmpPath,
			"cmdPath": util.FindSemaphore(),
		},
	}

	if util.UpdateAvailable != nil {
		body["updateBody"] = string(blackfriday.MarkdownCommon([]byte(*util.UpdateAvailable.Body)))
	}

	util.WriteJSON(w, http.StatusOK, body)
}

func checkUpgrade(w http.ResponseWriter, r *http.Request) {
	if err := util.CheckUpdate(util.Version); err != nil {
		util.WriteJSON(w, 500, err)
		return
	}

	if util.UpdateAvailable != nil {
		getSystemInfo(w, r)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func doUpgrade(w http.ResponseWriter, r *http.Request) {
	util.LogError(util.DoUpgrade(util.Version))
}
