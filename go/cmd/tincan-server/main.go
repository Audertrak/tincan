// tincan/cmd/tincan-server/main.go
package main

import (
	"flag"
	"log"
	"tincan/internal/server" // Assuming your go.mod defines 'module tincan'
)

func main() {
	// Define a command-line flag for serving the web client
	serveWeb := flag.Bool("serveweb", true, "Serve the web client (default: true)")
	// You could add flags for webClientPath and httpServerPort here too if desired
	// webPath := flag.String("webpath", "clients/web", "Path to web client files")
	// httpPort := flag.String("httpport", ":8081", "Port for HTTP web server")

	flag.Parse() // Parse the command-line flags

	log.Println("Starting Tincan server...")
	if *serveWeb {
		log.Println("Web client serving is ENABLED.")
	} else {
		log.Println("Web client serving is DISABLED (headless mode).")
	}

	// Pass the flag to the server's Start function
	server.Start(*serveWeb) // Pass the boolean value

	log.Println("Tincan server shut down.")
}
