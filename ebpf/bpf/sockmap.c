#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <sys/socket.h>

// Definicao do mapa de sockets concorrente L4
struct {
    __uint(type, BPF_MAP_TYPE_SOCKHASH);
    __uint(max_entries, 65535);
    __type(key, int);
    __type(value, int);
} sock_map SEC(".maps");

// Hook Sockops: Intercepta sockets na conexao estabelecida e guarda-os no mapa
SEC("sockops")
int bpf_sockmap_ops(struct bpf_sock_ops *skops) {
    int op = (int)skops->op;
    
    // Filtra apenas conexoes TCP estabelecidas (ativas e passivas)
    if (op == BPF_SOCK_OPS_ACTIVE_ESTABLISHED_CB || op == BPF_SOCK_OPS_PASSIVE_ESTABLISHED_CB) {
        int key = 1; // Tupla simplificada de porta/IP para hash
        bpf_sock_hash_update(skops, &sock_map, &key, BPF_NOEXIST);
    }
    return 0;
}

// Hook sk_msg: Intercepta os dados enviados (sendmsg) no socket e redireciona via Sockmap
SEC("sk_msg")
int bpf_redirect_msg(struct sk_msg_md *msg) {
    int key = 1;
    // Redireciona stream do socket de forma acelerada in-Kernel (segmento L4)
    bpf_msg_redirect_hash(msg, &sock_map, &key, BPF_F_INGRESS);
    return SK_PASS;
}

char _license[] SEC("license") = "GPL";
