from __future__ import annotations

import ipaddress
import shutil
import subprocess
from dataclasses import dataclass, field
from pathlib import Path

from .process import is_alive, terminate_pid

DEFAULT_BASE_CIDR = "10.99.0.0/24"
PHASE1_NAMESPACE_ORDER = (
    ("hub", None, "hub"),
    ("hub-up", "hub", "hub-up"),
    ("internet", "hub", "internet"),
    ("fbcoord", "hub", "fbcoord"),
    ("fbnotify", "hub", "fbnotify"),
    ("node-1", "hub", "node"),
    ("node-2", "hub", "node"),
    ("upstream-1", "hub-up", "upstream"),
    ("upstream-2", "hub-up", "upstream"),
)
LINK_NAME_ORDER = (
    ("hub", "fbcoord", "hub-fbcoord", "fbcoord-peer"),
    ("hub", "node-1", "hub-node1", "node1-peer"),
    ("hub", "node-2", "hub-node2", "node2-peer"),
    ("hub", "internet", "hub-inet", "inet-hub"),
    ("internet", "hub-up", "inet-hubup", "hubup-inet"),
    ("hub-up", "upstream-1", "hubup-u1", "upstream1-peer"),
    ("hub-up", "upstream-2", "hubup-u2", "upstream2-peer"),
    ("hub", "fbnotify", "hub-fbnotify", "fbnotify-peer"),
)
PHASE1_LINK_COUNT = len(LINK_NAME_ORDER)
UPSTREAM_RETURN_ROUTE_LEAVES = ("fbcoord", "fbnotify", "node-1", "node-2")


@dataclass(slots=True)
class Namespace:
    name: str
    pid: int
    parent: str | None
    role: str


@dataclass(slots=True)
class Link:
    left_ns: str
    right_ns: str
    left_if: str
    right_if: str
    subnet: str
    left_ip: str
    right_ip: str


@dataclass(slots=True)
class Topology:
    work_dir: str
    namespaces: dict[str, Namespace]
    links: list[Link]
    base_cidr: str
    clients: dict[str, str] = field(default_factory=dict)
    next_subnet_index: int = 0


def which(binary: str) -> str | None:
    return shutil.which(binary)


def allocate_subnets(base_cidr: str, count: int) -> list[ipaddress.IPv4Network]:
    network = ipaddress.ip_network(base_cidr)
    if not isinstance(network, ipaddress.IPv4Network):
        raise RuntimeError(f"base CIDR must be IPv4: {base_cidr}")
    if network.prefixlen > 30:
        raise RuntimeError(f"base CIDR too small for /30 allocation: {base_cidr}")
    subnets = list(network.subnets(new_prefix=30))
    if len(subnets) < count:
        raise RuntimeError(f"base CIDR {base_cidr} yields only {len(subnets)} /30 subnets; need {count}")
    return subnets[:count]


def allocate_next_subnet(base_cidr: str, next_subnet_index: int) -> tuple[ipaddress.IPv4Network, int]:
    subnets = allocate_subnets(base_cidr, next_subnet_index + 1)
    return subnets[next_subnet_index], next_subnet_index + 1


def compute_namespace_order(client_names: list[str] | tuple[str, ...] | set[str]) -> tuple[tuple[str, str | None, str], ...]:
    ordered = list(PHASE1_NAMESPACE_ORDER)
    sorted_names = sorted(client_names)
    if sorted_names:
        ordered.append(("client-edge", "hub", "client-edge"))
        ordered.extend((name, "client-edge", "client") for name in sorted_names)
    return tuple(ordered)


def compute_link_order(client_names: list[str] | tuple[str, ...] | set[str]) -> tuple[tuple[str, str, str, str], ...]:
    ordered = list(LINK_NAME_ORDER)
    sorted_names = sorted(client_names)
    if sorted_names:
        ordered.append(("internet", "client-edge", "inet-cedge", "cedge-inet"))
        for index, name in enumerate(sorted_names, start=1):
            ordered.append(("client-edge", name, f"cedge-c{index}", f"c{index}-peer"))
    return tuple(ordered)


def default_links(base_cidr: str = DEFAULT_BASE_CIDR, *, client_names: list[str] | tuple[str, ...] | set[str] = ()) -> list[Link]:
    link_order = compute_link_order(client_names)
    subnets = allocate_subnets(base_cidr, len(link_order))
    links: list[Link] = []
    for (left_ns, right_ns, left_if, right_if), subnet in zip(link_order, subnets, strict=True):
        hosts = list(subnet.hosts())
        links.append(
            Link(
                left_ns=left_ns,
                right_ns=right_ns,
                left_if=left_if,
                right_if=right_if,
                subnet=str(subnet),
                left_ip=str(hosts[0]),
                right_ip=str(hosts[1]),
            )
        )
    return links


def build_link(
    left_ns: str,
    right_ns: str,
    left_if: str,
    right_if: str,
    subnet: ipaddress.IPv4Network,
) -> Link:
    hosts = list(subnet.hosts())
    return Link(
        left_ns=left_ns,
        right_ns=right_ns,
        left_if=left_if,
        right_if=right_if,
        subnet=str(subnet),
        left_ip=str(hosts[0]),
        right_ip=str(hosts[1]),
    )


def nsenter_command(pid: int, args: list[str]) -> list[str]:
    return [
        "nsenter",
        "--preserve-credentials",
        "--keep-caps",
        "-t",
        str(pid),
        "-U",
        "-n",
        "--",
        *args,
    ]


def nsenter_run(pid: int, args: list[str]) -> subprocess.CompletedProcess[str]:
    command = nsenter_command(pid, args)
    try:
        return subprocess.run(command, check=True, capture_output=True, text=True)
    except subprocess.CalledProcessError as exc:
        stdout = exc.stdout.strip()
        stderr = exc.stderr.strip()
        details = []
        if stdout:
            details.append(f"stdout={stdout}")
        if stderr:
            details.append(f"stderr={stderr}")
        suffix = f" ({'; '.join(details)})" if details else ""
        raise RuntimeError(f"command failed: {' '.join(command)}{suffix}") from exc


def create_hub(name: str) -> Namespace:
    return _launch_namespace(
        [
            "unshare",
            "-Urn",
            "--kill-child=SIGTERM",
            "bash",
            "-lc",
            "echo $$; exec sleep infinity",
        ],
        name=name,
        parent=None,
        role="hub" if name == "hub" else "hub-up",
    )


def create_child(parent: Namespace, name: str, role: str) -> Namespace:
    return _launch_namespace(
        [
            "nsenter",
            "--preserve-credentials",
            "--keep-caps",
            "-t",
            str(parent.pid),
            "-U",
            "-n",
            "--",
            "unshare",
            "-n",
            "--kill-child=SIGTERM",
            "bash",
            "-lc",
            "echo $$; exec sleep infinity",
        ],
        name=name,
        parent=parent.name,
        role=role,
    )


def destroy(namespace: Namespace) -> None:
    terminate_pid(namespace.pid, timeout_sec=5)


def build_topology(
    work_dir: str,
    base_cidr: str = DEFAULT_BASE_CIDR,
    *,
    client_specs: dict[str, str] | None = None,
) -> Topology:
    Path(work_dir).mkdir(parents=True, exist_ok=True)
    namespaces: dict[str, Namespace] = {}
    client_specs = dict(client_specs or {})
    client_names = sorted(client_specs)
    links = default_links(base_cidr, client_names=client_names)

    try:
        for name, parent_name, role in compute_namespace_order(client_names):
            if parent_name is None:
                namespace = create_hub(name)
            else:
                namespace = create_child(namespaces[parent_name], name, role)
            namespaces[name] = namespace

        for link in links:
            create_veth(link, namespaces[link.left_ns], namespaces[link.right_ns])

        router_names = ["hub", "internet", "hub-up"]
        if client_names:
            router_names.append("client-edge")
        for router in router_names:
            enable_forwarding(namespaces[router].pid)

        for leaf in ("fbcoord", "fbnotify", "node-1", "node-2"):
            link = find_link(links, "hub", leaf)
            add_default_route(namespaces[leaf].pid, link.left_ip, link.right_if)

        for leaf in ("upstream-1", "upstream-2"):
            link = find_link(links, "hub-up", leaf)
            add_default_route(namespaces[leaf].pid, link.left_ip, link.right_if)

        for client_name in client_names:
            identity_ip = client_specs[client_name]
            link = find_link(links, "client-edge", client_name)
            add_identity_ip(namespaces[client_name].pid, identity_ip)
            add_default_route(
                namespaces[client_name].pid,
                link.left_ip,
                link.right_if,
                src=identity_ip,
            )

        hub_to_internet = find_link(links, "hub", "internet")
        internet_to_hub_up = find_link(links, "internet", "hub-up")

        for leaf in ("upstream-1", "upstream-2"):
            link = find_link(links, "hub-up", leaf)
            add_route(namespaces["hub"].pid, link.subnet, hub_to_internet.right_ip, hub_to_internet.left_if)
            add_route(namespaces["internet"].pid, link.subnet, internet_to_hub_up.right_ip, internet_to_hub_up.left_if)

        for leaf in UPSTREAM_RETURN_ROUTE_LEAVES:
            link = find_link(links, "hub", leaf)
            add_route(namespaces["hub-up"].pid, link.subnet, internet_to_hub_up.left_ip, internet_to_hub_up.right_if)
            add_route(namespaces["internet"].pid, link.subnet, hub_to_internet.left_ip, hub_to_internet.right_if)

        if client_names:
            internet_to_client_edge = find_link(links, "internet", "client-edge")
            add_default_route(
                namespaces["client-edge"].pid,
                internet_to_client_edge.left_ip,
                internet_to_client_edge.right_if,
            )
            for client_name in client_names:
                identity_ip = client_specs[client_name]
                link = find_link(links, "client-edge", client_name)
                add_route(
                    namespaces["client-edge"].pid,
                    f"{identity_ip}/32",
                    link.right_ip,
                    link.left_if,
                )
                add_route(
                    namespaces["internet"].pid,
                    f"{identity_ip}/32",
                    internet_to_client_edge.right_ip,
                    internet_to_client_edge.left_if,
                )
                add_route(
                    namespaces["hub"].pid,
                    f"{identity_ip}/32",
                    hub_to_internet.right_ip,
                    hub_to_internet.left_if,
                )

        return Topology(
            work_dir=str(Path(work_dir)),
            namespaces=namespaces,
            links=links,
            base_cidr=base_cidr,
            clients=client_specs,
            next_subnet_index=len(links),
        )
    except Exception:
        destroy_topology(
            Topology(
                work_dir=str(Path(work_dir)),
                namespaces=namespaces,
                links=links,
                base_cidr=base_cidr,
                clients=client_specs,
                next_subnet_index=len(links),
            )
        )
        raise


def destroy_topology(topology: Topology) -> None:
    if topology is None:
        return
    namespaces = topology.namespaces

    def depth(namespace: Namespace) -> int:
        level = 0
        current = namespace
        while current.parent:
            level += 1
            current = namespaces[current.parent]
        return level

    for namespace in sorted(namespaces.values(), key=lambda item: (depth(item), item.name), reverse=True):
        destroy(namespace)


def verify_connectivity(topology: Topology) -> None:
    namespaces = topology.namespaces
    link_hub_internet = find_link(topology.links, "hub", "internet")
    link_internet_hubup = find_link(topology.links, "internet", "hub-up")
    checks = [
        ("node-1", find_link(topology.links, "hub-up", "upstream-1").right_ip),
        ("node-1", find_link(topology.links, "hub-up", "upstream-2").right_ip),
        ("node-2", find_link(topology.links, "hub-up", "upstream-1").right_ip),
        ("fbcoord", find_link(topology.links, "hub-up", "upstream-1").right_ip),
        ("fbcoord", find_link(topology.links, "hub-up", "upstream-2").right_ip),
        ("fbnotify", find_link(topology.links, "hub-up", "upstream-1").right_ip),
        ("fbnotify", find_link(topology.links, "hub-up", "upstream-2").right_ip),
        ("node-1", find_link(topology.links, "hub", "fbnotify").right_ip),
        ("internet", link_hub_internet.left_ip),
        ("internet", link_internet_hubup.right_ip),
    ]
    if topology.clients:
        node_transport_ip = find_link(topology.links, "hub", "node-1").right_ip
        checks.extend((client_name, node_transport_ip) for client_name in sorted(topology.clients))
    for source, target in checks:
        nsenter_run(namespaces[source].pid, ["ping", "-c", "1", "-W", "1", target])


def add_route(pid: int, destination: str, via: str, dev: str) -> None:
    nsenter_run(pid, ["ip", "route", "replace", destination, "via", via, "dev", dev])


def delete_route(pid: int, destination: str, *, via: str | None = None, dev: str | None = None) -> None:
    command = ["ip", "route", "del", destination]
    if via is not None:
        command.extend(["via", via])
    if dev is not None:
        command.extend(["dev", dev])
    try:
        nsenter_run(pid, command)
    except RuntimeError as exc:
        message = str(exc)
        if "No such process" in message or "No such file or directory" in message:
            return
        raise


def add_default_route(pid: int, gateway: str, dev: str, *, src: str | None = None) -> None:
    command = ["ip", "route", "replace", "default", "via", gateway, "dev", dev]
    if src:
        command.extend(["src", src])
    nsenter_run(pid, command)


def add_identity_ip(pid: int, ip: str) -> None:
    nsenter_run(pid, ["ip", "addr", "add", f"{ip}/32", "dev", "lo"])


def enable_forwarding(pid: int) -> None:
    nsenter_run(pid, ["sysctl", "-w", "net.ipv4.ip_forward=1"])


def create_veth(link: Link, left: Namespace, right: Namespace) -> None:
    prefixlen = ipaddress.ip_network(link.subnet).prefixlen
    nsenter_run(left.pid, ["ip", "link", "add", link.left_if, "type", "veth", "peer", "name", link.right_if])
    nsenter_run(left.pid, ["ip", "link", "set", link.right_if, "netns", f"/proc/{right.pid}/ns/net"])

    nsenter_run(left.pid, ["ip", "addr", "add", f"{link.left_ip}/{prefixlen}", "dev", link.left_if])
    nsenter_run(left.pid, ["ip", "link", "set", link.left_if, "up"])

    nsenter_run(right.pid, ["ip", "addr", "add", f"{link.right_ip}/{prefixlen}", "dev", link.right_if])
    nsenter_run(right.pid, ["ip", "link", "set", link.right_if, "up"])


def _next_client_interface_index(topology: Topology) -> int:
    existing = [
        int(link.left_if.removeprefix("cedge-c"))
        for link in topology.links
        if link.left_ns == "client-edge" and link.right_ns.startswith("client-") and link.left_if.startswith("cedge-c")
    ]
    return max(existing, default=0) + 1


def create_client_edge(topology: Topology) -> tuple[Namespace, Link, int]:
    if "client-edge" in topology.namespaces:
        return topology.namespaces["client-edge"], find_link(topology.links, "internet", "client-edge"), topology.next_subnet_index

    namespace = create_child(topology.namespaces["hub"], "client-edge", "client-edge")
    subnet, next_subnet_index = allocate_next_subnet(topology.base_cidr, topology.next_subnet_index)
    link = build_link("internet", "client-edge", "inet-cedge", "cedge-inet", subnet)
    try:
        create_veth(link, topology.namespaces["internet"], namespace)
        enable_forwarding(namespace.pid)
        add_default_route(namespace.pid, link.left_ip, link.right_if)
    except Exception:
        destroy(namespace)
        raise

    topology.namespaces["client-edge"] = namespace
    topology.links.append(link)
    topology.next_subnet_index = next_subnet_index
    return namespace, link, next_subnet_index


def remove_client_edge(topology: Topology) -> None:
    namespace = topology.namespaces.pop("client-edge", None)
    if namespace is None:
        return
    try:
        destroy(namespace)
    finally:
        topology.links = [
            link
            for link in topology.links
            if not (link.left_ns == "internet" and link.right_ns == "client-edge")
        ]


def create_client_namespace(topology: Topology, client_name: str, identity_ip: str) -> tuple[Namespace, Link, int]:
    if "client-edge" not in topology.namespaces:
        raise RuntimeError("client-edge is not present in topology")
    if client_name in topology.namespaces:
        raise RuntimeError(f"client namespace already exists: {client_name}")

    namespace = create_child(topology.namespaces["client-edge"], client_name, "client")
    subnet, next_subnet_index = allocate_next_subnet(topology.base_cidr, topology.next_subnet_index)
    link_index = _next_client_interface_index(topology)
    link = build_link("client-edge", client_name, f"cedge-c{link_index}", f"c{link_index}-peer", subnet)
    internet_to_client_edge = find_link(topology.links, "internet", "client-edge")
    hub_to_internet = find_link(topology.links, "hub", "internet")
    try:
        create_veth(link, topology.namespaces["client-edge"], namespace)
        add_identity_ip(namespace.pid, identity_ip)
        add_default_route(namespace.pid, link.left_ip, link.right_if, src=identity_ip)
        add_route(topology.namespaces["client-edge"].pid, f"{identity_ip}/32", link.right_ip, link.left_if)
        add_route(
            topology.namespaces["internet"].pid,
            f"{identity_ip}/32",
            internet_to_client_edge.right_ip,
            internet_to_client_edge.left_if,
        )
        add_route(
            topology.namespaces["hub"].pid,
            f"{identity_ip}/32",
            hub_to_internet.right_ip,
            hub_to_internet.left_if,
        )
    except Exception:
        destroy(namespace)
        raise

    topology.namespaces[client_name] = namespace
    topology.links.append(link)
    topology.clients[client_name] = identity_ip
    topology.next_subnet_index = next_subnet_index
    return namespace, link, next_subnet_index


def remove_client_namespace(topology: Topology, client_name: str) -> None:
    if client_name not in topology.namespaces or client_name not in topology.clients:
        raise KeyError(f"unknown client namespace: {client_name}")

    identity_ip = topology.clients[client_name]
    link = find_link(topology.links, "client-edge", client_name)
    internet_to_client_edge = find_link(topology.links, "internet", "client-edge")
    hub_to_internet = find_link(topology.links, "hub", "internet")

    delete_route(topology.namespaces["client-edge"].pid, f"{identity_ip}/32", via=link.right_ip, dev=link.left_if)
    delete_route(
        topology.namespaces["internet"].pid,
        f"{identity_ip}/32",
        via=internet_to_client_edge.right_ip,
        dev=internet_to_client_edge.left_if,
    )
    delete_route(
        topology.namespaces["hub"].pid,
        f"{identity_ip}/32",
        via=hub_to_internet.right_ip,
        dev=hub_to_internet.left_if,
    )

    namespace = topology.namespaces.pop(client_name)
    try:
        destroy(namespace)
    finally:
        topology.links = [
            existing
            for existing in topology.links
            if not (existing.left_ns == "client-edge" and existing.right_ns == client_name)
        ]
        del topology.clients[client_name]


def find_link(links: list[Link], left_ns: str, right_ns: str) -> Link:
    for link in links:
        if link.left_ns == left_ns and link.right_ns == right_ns:
            return link
    raise KeyError(f"link not found: {left_ns} -> {right_ns}")


def _launch_namespace(command: list[str], name: str, parent: str | None, role: str) -> Namespace:
    process = subprocess.Popen(
        command,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        start_new_session=True,
    )
    try:
        pid_line = (process.stdout.readline() if process.stdout else "").strip()
        if not pid_line:
            stderr = process.stderr.read().strip() if process.stderr else ""
            terminate_pid(process.pid, timeout_sec=1)
            raise RuntimeError(f"failed to read namespace pid for {name}: {stderr}")
        pid = int(pid_line)
    except Exception:
        if process.stdout:
            process.stdout.close()
        if process.stderr:
            process.stderr.close()
        raise

    if process.stdout:
        process.stdout.close()
    if process.stderr:
        process.stderr.close()

    if not is_alive(pid):
        terminate_pid(process.pid, timeout_sec=1)
        raise RuntimeError(f"namespace process for {name} is not alive after launch")

    try:
        nsenter_run(pid, ["ip", "link", "set", "lo", "up"])
    except Exception:
        terminate_pid(pid, timeout_sec=1)
        raise
    return Namespace(name=name, pid=pid, parent=parent, role=role)
