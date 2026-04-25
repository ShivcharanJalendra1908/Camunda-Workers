package main
import (
    "fmt"
    "net/smtp"
    "crypto/tls"
)
func main() {
    client, err := smtp.Dial("email-smtp.us-east-1.amazonaws.com:587")
    if err != nil { fmt.Println("Dial error:", err); return }
    tlsConfig := &tls.Config{ServerName: "email-smtp.us-east-1.amazonaws.com"}
    if err = client.StartTLS(tlsConfig); err != nil { fmt.Println("TLS error:", err); return }
    auth := smtp.PlainAuth("", "AKIASS3JXDPVWDZXP5P7", "BN+mVdSkLw2+aws3SxJijza7mCZJEvp4h0DqP56cmZms", "email-smtp.us-east-1.amazonaws.com")
    if err = client.Auth(auth); err != nil { fmt.Println("Auth FAILED:", err); return }
    fmt.Println("SUCCESS!")
    client.Quit()
}
