// Fixtures of internal/scan/rules/drydock/csharp.yaml: `ruleid:` marks a line the rule must find, `ok:` one it must not.
using System.Diagnostics;
using System.Net.Http;
using System.Runtime.Serialization.Formatters.Binary;
using System.Security.Cryptography;
using Microsoft.Data.SqlClient;

class Fixtures {
    void Run(SqlConnection conn, string name, HttpClientHandler handler) {
        // ruleid: drydock.cs.sql-built-from-data
        var cmd = new SqlCommand("SELECT * FROM Users WHERE Name = '" + name + "'", conn);
        // ruleid: drydock.cs.sql-built-from-data
        var cmd2 = new SqlCommand($"SELECT * FROM Users WHERE Name = '{name}'", conn);
        // ok: drydock.cs.sql-built-from-data
        var cmd3 = new SqlCommand("SELECT * FROM Users WHERE Name = @name", conn);

        // ruleid: drydock.cs.binary-formatter
        var bf = new BinaryFormatter();

        // ruleid: drydock.cs.weak-hash
        var h = MD5.Create();
        // ok: drydock.cs.weak-hash
        var h2 = SHA256.Create();

        // ruleid: drydock.cs.tls-certificate-check-off
        handler.ServerCertificateCustomValidationCallback = (a, b, c, d) => true;

        // ruleid: drydock.cs.process-shell-built-from-data
        Process.Start("cmd.exe", "/c " + name);
        // ok: drydock.cs.process-shell-built-from-data
        Process.Start("git", "status");
    }
}
