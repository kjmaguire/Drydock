// Fixtures of internal/scan/rules/drydock/java.yaml: `ruleid:` marks a line the rule must find, `ok:` one it must not.
import java.io.*;
import java.security.MessageDigest;
import java.sql.*;
import javax.net.ssl.*;

class Fixtures {
    void run(Connection c, Statement st, String name, InputStream in) throws Exception {
        // ruleid: drydock.java.sql-built-from-data
        st.executeQuery("SELECT * FROM users WHERE name = '" + name + "'");
        // ok: drydock.java.sql-built-from-data
        PreparedStatement ps = c.prepareStatement("SELECT * FROM users WHERE name = ?");

        // ruleid: drydock.java.command-built-from-data
        Runtime.getRuntime().exec("git log " + name);
        // ok: drydock.java.command-built-from-data
        new ProcessBuilder("git", "log", name).start();

        // ruleid: drydock.java.weak-hash
        MessageDigest.getInstance("MD5");
        // ok: drydock.java.weak-hash
        MessageDigest.getInstance("SHA-256");

        ObjectInputStream ois = new ObjectInputStream(in);
        // ruleid: drydock.java.object-input-stream
        Object o = ois.readObject();

        // ruleid: drydock.java.trust-all-certificates
        HttpsURLConnection.setDefaultHostnameVerifier((String h, SSLSession s) -> true);
    }
}
