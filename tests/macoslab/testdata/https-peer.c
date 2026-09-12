/* Test-only isolated HTTPS peer. libslirp owns TCP; no stack is implemented here.
 * Only feth43 frames from 192.168.250.2 to 192.168.250.1:443 are admitted.
 * Restricted networking forwards exclusively to ./https.sock after dropping root.
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
#include <sys/stat.h>
#include <time.h>
#include <libslirp.h>

static const uint8_t source_ip[4] = {192,168,250,2};
static const uint8_t peer_ip[4] = {192,168,250,1};
static uint8_t source_mac[6], peer_mac[6];
static void die(const char *why) { fprintf(stderr,"HTTPS lab peer: %s\n",why); exit(1); }
static uint16_t word(const uint8_t *p) { return (uint16_t)((p[0]<<8)|p[1]); }
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

static pcap_t *capture;
static unsigned sent, received;
static struct pollfd fds[32];
static unsigned nfds;
static int64_t clock_ns(void *opaque) {
    (void)opaque; struct timespec ts;
    if (clock_gettime(CLOCK_MONOTONIC,&ts)) die("clock failed");
    return (int64_t)ts.tv_sec*1000000000+ts.tv_nsec;
}
static void guest_error(const char *message,void *opaque) {
    (void)message; (void)opaque; die("invalid guest traffic");
}
static ssize_t send_packet(const void *data,size_t len,void *opaque) {
    (void)opaque;
    if (++sent>1024 || len>1600 || len<14) die("output frame bound exceeded");
    if (capture) {
        // Keep the fixture's link identity stable across TCP, DNS and ICMP peers.
        // libslirp's virtual MAC must never replace feth43 in the host ARP cache.
        uint8_t frame[1600]; memcpy(frame,data,len); memcpy(frame+6,peer_mac,6);
        if(word(frame+12)==0x0806 && len>=42) memcpy(frame+22,peer_mac,6);
        if(pcap_inject(capture,frame,len)!=(int)len) die("frame injection failed");
    }
    return (ssize_t)len;
}
static void notify(void *opaque) { (void)opaque; }
static void register_socket(slirp_os_socket fd,void *opaque) { (void)fd;(void)opaque; }
static int add_poll(slirp_os_socket fd,int events,void *opaque) {
    (void)opaque; if(nfds>=32) die("socket count exceeded");
    short flags=0;
    if(events&SLIRP_POLL_IN) flags|=POLLIN;
    if(events&SLIRP_POLL_OUT) flags|=POLLOUT;
    if(events&SLIRP_POLL_PRI) flags|=POLLPRI;
    fds[nfds]=(struct pollfd){fd,flags,0}; return (int)nfds++;
}
static int get_events(int i,void *opaque) {
    (void)opaque; if(i<0 || (unsigned)i>=nfds) die("invalid poll index");
    short f=fds[i].revents; int out=0;
    if(f&POLLIN) out|=SLIRP_POLL_IN;
    if(f&POLLOUT) out|=SLIRP_POLL_OUT;
    if(f&POLLPRI) out|=SLIRP_POLL_PRI;
    if(f&POLLERR) out|=SLIRP_POLL_ERR;
    if(f&POLLHUP) out|=SLIRP_POLL_HUP;
    return out;
}
static Slirp *stack(void) {
    SlirpConfig cfg={0}; cfg.version=6; cfg.restricted=1; cfg.in_enabled=true;
    cfg.disable_dns=true; cfg.disable_dhcp=true; cfg.disable_host_loopback=true;
    cfg.if_mtu=1500; cfg.if_mru=1500;
    inet_pton(AF_INET,"192.168.250.0",&cfg.vnetwork);
    inet_pton(AF_INET,"255.255.255.0",&cfg.vnetmask);
    inet_pton(AF_INET,"192.168.250.254",&cfg.vhost);
    static SlirpCb cb; cb.send_packet=send_packet; cb.guest_error=guest_error;
    cb.clock_get_ns=clock_ns; cb.notify=notify;
    cb.register_poll_socket=register_socket; cb.unregister_poll_socket=register_socket;
    Slirp *s=slirp_new(&cfg,&cb,NULL);
    struct in_addr target; inet_pton(AF_INET,"192.168.250.1",&target);
    if(!s || slirp_add_unix(s,"https.sock",&target,443)) die("restricted Unix forwarding failed");
    return s;
}
static int admitted(const uint8_t *p,size_t n) {
    if(n<42 || n>1514 || memcmp(p+6,source_mac,6)) return 0;
    if(word(p+12)==0x0806) {
        const uint8_t *a=p+14;
        return word(a)==1 && word(a+2)==0x0800 && a[4]==6 && a[5]==4 &&
            (word(a+6)==1 || word(a+6)==2) && !memcmp(a+8,source_mac,6) &&
            !memcmp(a+14,source_ip,4) && (!memcmp(a+24,peer_ip,4) ||
            (!memcmp(a+24,peer_ip,3) && a[27]==254));
    }
    if(word(p+12)!=0x0800 || n<54 || p[14]!=0x45 || p[23]!=6 ||
        (word(p+20)&0x3fff) || word(p+16)+14>n || word(p+16)<40 ||
        memcmp(p+26,source_ip,4) || memcmp(p+30,peer_ip,4) || word(p+36)!=443) return 0;
    return 1;
}
int main(int argc,char **argv) {
    if(argc!=2 || (strcmp(argv[1],"check") && strcmp(argv[1],"https"))) die("fixed mode required");
    if(!strcmp(argv[1],"check")) {
        Slirp *s=stack(); slirp_cleanup(s);
        printf("libslirp %s restricted Unix peer initialized\n",slirp_version_string()); return 0;
    }
    interfaces(0);
    if(geteuid()!=0) die("BPF setup requires sudo");
    uid_t uid=(uid_t)identity("SUDO_UID"); gid_t gid=(gid_t)identity("SUDO_GID");
    struct stat socket_stat;
    if(lstat("https.sock",&socket_stat) || !S_ISSOCK(socket_stat.st_mode) || socket_stat.st_uid!=uid || (socket_stat.st_mode&0077)) die("private owned Unix socket required");
    alarm(40);
    char error[PCAP_ERRBUF_SIZE]; capture=pcap_create("feth43",error);
    if(!capture || pcap_set_snaplen(capture,1600) || pcap_set_promisc(capture,0) ||
        pcap_set_timeout(capture,10) || pcap_set_immediate_mode(capture,1) ||
        pcap_activate(capture)<0 || pcap_datalink(capture)!=DLT_EN10MB || pcap_setnonblock(capture,1,error)) die("BPF setup failed");
    struct bpf_program filter;
    if(pcap_compile(capture,&filter,"arp or (tcp and src host 192.168.250.2 and dst host 192.168.250.1 and dst port 443)",1,PCAP_NETMASK_UNKNOWN) || pcap_setfilter(capture,&filter)) die("BPF filter failed");
    pcap_freecode(&filter);
    if(setgroups(0,NULL) || setgid(gid) || setuid(uid) || geteuid()==0 || getuid()!=uid) die("privilege drop failed");
    Slirp *s=stack(); setvbuf(stdout,NULL,_IOLBF,0);
    printf("{\"event\":\"ready\",\"uid\":%u}\n",(unsigned)geteuid());
    int64_t until=clock_ns(NULL)+30000000000LL;
    for(;;) {
        if(clock_ns(NULL)>=until) die("lifetime exceeded");
        struct pollfd input={STDIN_FILENO,POLLIN|POLLHUP,0};
        if(poll(&input,1,0)<0) die("lifetime input failed");
        if(input.revents&(POLLIN|POLLHUP)) {char c;if(read(STDIN_FILENO,&c,1)==0)break;die("no input commands accepted");}
        struct pcap_pkthdr *h=NULL; const u_char *p=NULL;
        int got=pcap_next_ex(capture,&h,&p);
        if(got<0) die("capture failed");
        if(got) {
            if(++received>2048) die("input frame count exceeded");
            if(h->caplen==h->len && admitted(p,h->caplen)) slirp_input(s,p,(int)h->caplen);
        }
        nfds=0; uint32_t timeout=1;
        slirp_pollfds_fill_socket(s,&timeout,add_poll,NULL);
        if(poll(fds,nfds,(int)(timeout>1?1:timeout))<0) die("socket poll failed");
        slirp_pollfds_poll(s,0,get_events,NULL);
    }
    slirp_cleanup(s);pcap_close(capture);
    printf("{\"event\":\"summary\",\"bytes\":%u}\n",sent);
    return 0;
}
