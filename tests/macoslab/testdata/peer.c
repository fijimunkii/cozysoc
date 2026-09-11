/* Test-only peer for one isolated feth pair; never installed with Cozy SOC.
 * System libpcap handles BPF. Only fixed synthetic ARP/ICMP traffic is accepted.
 * Open BPF with sudo, then permanently drop privilege before processing frames.
 */
#include <arpa/inet.h>
#include <grp.h>
#include <ifaddrs.h>
#include <net/if.h>
#include <net/if_dl.h>
#include <pcap/pcap.h>
#include <poll.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static const uint8_t source_ip[4] = {192, 168, 250, 2};
static const uint8_t peer_ip[4] = {192, 168, 250, 1};
static uint8_t source_mac[6], peer_mac[6];
static void die(const char *why) { fprintf(stderr, "lab peer: %s\n", why); exit(1); }
static uint16_t word(const uint8_t *p) { return ((uint16_t)p[0] << 8) | p[1]; }
static void put(uint8_t *p, uint16_t v) { p[0] = v >> 8; p[1] = v & 255; }
static uint16_t checksum(const uint8_t *p, size_t n) {
    uint32_t sum = 0;
    while (n >= 2) { sum += word(p); p += 2; n -= 2; }
    if (n) sum += (uint32_t)*p << 8;
    while (sum >> 16) sum = (sum & 65535) + (sum >> 16);
    return (uint16_t)~sum;
}
static unsigned long identity(const char *name) {
    const char *s = getenv(name); char *end = NULL;
    if (!s || !*s) die("missing invoking identity");
    unsigned long v = strtoul(s, &end, 10);
    if (*end || !v || v > 2147483647) die("invalid invoking identity");
    return v;
}
static void interfaces(int check_only) {
    struct ifaddrs *all = NULL;
    if (getifaddrs(&all)) die("cannot read interface metadata");
    int src = 0, left = 0, right = 0;
    for (struct ifaddrs *a = all; a; a = a->ifa_next) {
        if (!a->ifa_addr) continue;
        if (a->ifa_addr->sa_family == AF_INET) {
            const uint8_t *ip = (const uint8_t *)&((struct sockaddr_in *)a->ifa_addr)->sin_addr;
            if (check_only) {
                if (!a->ifa_netmask) die("missing IPv4 mask");
                const uint8_t *mask = (const uint8_t *)&((struct sockaddr_in *)a->ifa_netmask)->sin_addr;
                int overlap = 1;
                for (int i = 0; i < 4; i++) {
                    uint8_t common = mask[i] & (i == 3 ? 0 : 255);
                    if ((ip[i] & common) != (source_ip[i] & common)) overlap = 0;
                }
                if (overlap) die("lab subnet overlaps an existing interface");
            } else {
                if (!memcmp(ip, peer_ip, 4)) die("peer must not be a local address");
                if (!strcmp(a->ifa_name, "feth43")) die("peer interface must have no IPv4 address");
                if (!memcmp(ip, source_ip, 4)) {
                    if (strcmp(a->ifa_name, "feth42")) die("source is not isolated");
                    src++;
                }
            }
        }
        if (a->ifa_addr->sa_family == AF_LINK &&
            (!strcmp(a->ifa_name, "feth42") || !strcmp(a->ifa_name, "feth43"))) {
            if (check_only) die("lab interface already exists");
            struct sockaddr_dl *dl = (struct sockaddr_dl *)a->ifa_addr;
            if (dl->sdl_alen != 6 || !(a->ifa_flags & IFF_UP)) die("invalid lab link");
            if (!strcmp(a->ifa_name, "feth42")) { memcpy(source_mac, LLADDR(dl), 6); left++; }
            else { memcpy(peer_mac, LLADDR(dl), 6); right++; }
        }
    }
    freeifaddrs(all);
    if (!check_only && (src != 1 || left != 1 || right != 1)) die("missing isolated fixture");
}
static void inject(pcap_t *pc, const uint8_t *p, size_t n) {
    if (pcap_inject(pc, p, n) != (int)n) die("frame injection failed");
}
int main(int argc, char **argv) {
    if (argc != 2) die("expected fixed peer mode");
    int check_only = !strcmp(argv[1], "check");
    int silent = !strcmp(argv[1], "silent"), wrong = !strcmp(argv[1], "wrong-nonce");
    if (!check_only && !silent && !wrong && strcmp(argv[1], "reply")) die("unknown mode");
    interfaces(check_only);
    if (check_only) return 0;
    if (geteuid() != 0) die("BPF setup requires sudo");
    uid_t uid = (uid_t)identity("SUDO_UID"); gid_t gid = (gid_t)identity("SUDO_GID");
    alarm(25); /* Hard lifetime even when the invoking test dies. */
    char error[PCAP_ERRBUF_SIZE];
    pcap_t *pc = pcap_create("feth43", error);
    if (!pc || pcap_set_snaplen(pc, 128) || pcap_set_promisc(pc, 0) ||
        pcap_set_timeout(pc, 50) || pcap_set_immediate_mode(pc, 1) || pcap_activate(pc) < 0 ||
        pcap_datalink(pc) != DLT_EN10MB || pcap_setnonblock(pc, 1, error)) die("BPF setup failed");
    struct bpf_program filter;
    if (pcap_compile(pc, &filter, "arp or (icmp and src host 192.168.250.2 and dst host 192.168.250.1)", 1, PCAP_NETMASK_UNKNOWN)) die("filter compilation failed");
    if (pcap_setfilter(pc, &filter)) die("filter installation failed");
    pcap_freecode(&filter);
    if (setgroups(0, NULL) || setgid(gid) || setuid(uid) || geteuid() == 0 || getuid() != uid) die("privilege drop failed");
    setvbuf(stdout, NULL, _IOLBF, 0);
    printf("{\"event\":\"ready\",\"uid\":%u}\n", (unsigned)geteuid());
    unsigned frames = 0, echoes = 0, arps = 0;
    for (;;) {
        struct pollfd input = {STDIN_FILENO, POLLIN | POLLHUP, 0};
        if (poll(&input, 1, 0) < 0) die("lifetime input failed");
        if (input.revents & (POLLIN | POLLHUP)) {
            char c; if (read(STDIN_FILENO, &c, 1) == 0) break;
            die("peer accepts no input commands");
        }
        struct pcap_pkthdr *header = NULL; const u_char *frame = NULL;
        int got = pcap_next_ex(pc, &header, &frame);
        if (got < 0) die("capture failed");
        if (!got) { usleep(1000); continue; }
        if (++frames > 64) die("frame budget exceeded");
        if (header->caplen != header->len || header->caplen < 42 || memcmp(frame + 6, source_mac, 6)) continue;
        if (word(frame + 12) == 0x0806) {
            const uint8_t *a = frame + 14;
            if (word(a) != 1 || word(a + 2) != 0x0800 || a[4] != 6 || a[5] != 4 ||
                word(a + 6) != 1 || memcmp(a + 8, source_mac, 6) ||
                memcmp(a + 14, source_ip, 4) || memcmp(a + 24, peer_ip, 4)) continue;
            if (++arps > 16) die("ARP reply budget exceeded");
            uint8_t out[60] = {0};
            memcpy(out, source_mac, 6); memcpy(out + 6, peer_mac, 6); put(out + 12, 0x0806);
            memcpy(out + 14, a, 8); put(out + 20, 2);
            memcpy(out + 22, peer_mac, 6); memcpy(out + 28, peer_ip, 4);
            memcpy(out + 32, source_mac, 6); memcpy(out + 38, source_ip, 4);
            inject(pc, out, sizeof(out));
            continue;
        }
        if (word(frame + 12) != 0x0800 || header->caplen != 74 || memcmp(frame, peer_mac, 6)) continue;
        const uint8_t *ip = frame + 14, *icmp = frame + 34;
        if (ip[0] != 0x45 || word(ip + 2) != 60 || (word(ip + 6) & 0x3fff) || ip[8] != 1 || ip[9] != 1 ||
            memcmp(ip + 12, source_ip, 4) || memcmp(ip + 16, peer_ip, 4) ||
            icmp[0] != 8 || icmp[1] || checksum(icmp, 40)) continue;
        if (++echoes > 3) die("echo budget exceeded");
        printf("{\"event\":\"echo\",\"seq\":%u,\"bytes\":40,\"at_us\":%llu}\n", word(icmp + 6),
               (unsigned long long)header->ts.tv_sec * 1000000 + header->ts.tv_usec);
        if (silent) continue;
        uint8_t out[74] = {0};
        memcpy(out, source_mac, 6); memcpy(out + 6, peer_mac, 6); put(out + 12, 0x0800);
        out[14] = 0x45; put(out + 16, 60); out[22] = 64; out[23] = 1;
        memcpy(out + 26, peer_ip, 4); memcpy(out + 30, source_ip, 4);
        put(out + 24, checksum(out + 14, 20));
        memcpy(out + 34, icmp, 40); out[34] = 0; out[36] = out[37] = 0;
        if (wrong) out[42] ^= 1;
        put(out + 36, checksum(out + 34, 40));
        inject(pc, out, sizeof(out));
    }
    printf("{\"event\":\"summary\",\"echoes\":%u,\"arps\":%u}\n", echoes, arps);
    pcap_close(pc);
    return 0;
}
