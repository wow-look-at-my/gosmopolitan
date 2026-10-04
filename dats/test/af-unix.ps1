$src = @'
using System;
using System.Runtime.InteropServices;
using System.Text;
public static class AfUnixProbe {
    [DllImport("ws2_32.dll")] public static extern int WSAStartup(ushort v, byte[] data);
    [DllImport("ws2_32.dll")] public static extern IntPtr WSASocketW(int af, int type, int proto, IntPtr pi, uint g, uint flags);
    [DllImport("ws2_32.dll")] public static extern int bind(IntPtr s, byte[] name, int namelen);
    [DllImport("ws2_32.dll")] public static extern int setsockopt(IntPtr s, int level, int optname, ref int optval, int optlen);
    [DllImport("ws2_32.dll")] public static extern int ioctlsocket(IntPtr s, int cmd, ref int arg);
    [DllImport("ws2_32.dll")] public static extern int WSAGetLastError();
    [DllImport("ws2_32.dll")] public static extern int closesocket(IntPtr s);
    public static void TryCase(string label, uint flags, string path, bool reuse, bool nonblock) {
        IntPtr s = WSASocketW(1, 1, 0, IntPtr.Zero, 0, flags);
        if (s == (IntPtr)(-1L)) { Console.WriteLine(label + ": WSASocketW failed " + WSAGetLastError()); return; }
        if (reuse) {
            int one = 1;
            int rr = setsockopt(s, 0xffff, 0x0004, ref one, 4);
            Console.WriteLine(label + ": setsockopt SO_REUSEADDR " + (rr == 0 ? "OK" : "failed " + WSAGetLastError()));
        }
        if (nonblock) {
            int nb = 1;
            ioctlsocket(s, unchecked((int)0x8004667E), ref nb);
        }
        byte[] p = Encoding.UTF8.GetBytes(path);
        byte[] sa = new byte[110];
        sa[0] = 1; sa[1] = 0;
        Array.Copy(p, 0, sa, 2, p.Length);
        int r = bind(s, sa, 2 + p.Length + 1);
        Console.WriteLine(r == 0 ? label + ": bind OK" : label + ": bind failed " + WSAGetLastError());
        closesocket(s);
        try { System.IO.File.Delete(path); } catch {}
    }
    public static void Run(string dir) {
        byte[] d = new byte[512]; WSAStartup(0x0202, d);
        TryCase("native plain (flags 0x80)", 0x80, System.IO.Path.Combine(dir, "afu-1.sock"), false, false);
        TryCase("native +SO_REUSEADDR", 0x80, System.IO.Path.Combine(dir, "afu-2.sock"), true, false);
        TryCase("native +FIONBIO", 0x80, System.IO.Path.Combine(dir, "afu-3.sock"), false, true);
        TryCase("native +SO_REUSEADDR+FIONBIO", 0x80, System.IO.Path.Combine(dir, "afu-4.sock"), true, true);
        Console.WriteLine("GetTempPath=" + System.IO.Path.GetTempPath());
        TryCase("native plain at GetTempPath", 0x80, System.IO.Path.Combine(System.IO.Path.GetTempPath(), "afu-5.sock"), false, false);
    }
}
'@
try {
  Add-Type -TypeDefinition $src
  [AfUnixProbe]::Run($env:RUNNER_TEMP)
} catch { Write-Host "native probe error: $_" }
try {
  $mp = Join-Path $env:RUNNER_TEMP 'afu-managed.sock'
  $sock = [System.Net.Sockets.Socket]::new([System.Net.Sockets.AddressFamily]::Unix, [System.Net.Sockets.SocketType]::Stream, [System.Net.Sockets.ProtocolType]::Unspecified)
  $ep = [System.Net.Sockets.UnixDomainSocketEndPoint]::new($mp)
  $sock.Bind($ep)
  Write-Host 'managed .NET UnixDomainSocket: bind OK'
  $ok = $true
  $sock.Close()
  Remove-Item $mp -ErrorAction SilentlyContinue
} catch { Write-Host "managed .NET UnixDomainSocket: $_" }
if ($ok) { exit 0 } else { exit 1 }
