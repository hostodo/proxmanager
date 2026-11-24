from flask import Flask, request, make_response
import os
import json
import subprocess
from ruamel.yaml import YAML

from utils.exceptions import BadRequestException

app = Flask(__name__)

def authenticate():
    headers = request.headers
    token = headers.get('Authorization')

    if not token:
        raise BadRequestException("Missing token")

    token = token.replace('Bearer ', '')
    if token != os.getenv('API_TOKEN'):
        raise BadRequestException("Invalid token")


@app.route('/status', methods=['GET'])
def status():
    return {
        "status": "ok"
    }

@app.route('/node/snippets/network/<vm_id>', methods=['POST'])
def snippets_network_vmid_post(vm_id):
    authenticate()
    body = request.json
    is_centos = body.get('is_centos')
    explicit_routes = body.get('explicit_default_routes', False)
    if not isinstance(explicit_routes, bool) or (explicit_routes and is_centos):
        return {"error": "explicit_default_routes must be a boolean and is unsupported for CentOS or AlmaLinux"}, 400
    network_device_name = body.get('network_device_name', 'eth0')
    ipv4_addresses = body.get('ipv4_addresses') or []
    ipv6_addresses = body.get('ipv6_addresses') or []
    mac_address = body.get('mac_address')

    if not ipv4_addresses or not mac_address:
        return {
            "error": "ipv4_addresses and mac_address are required"
        }, 400

    v2_addresses = []
    for ipv4_address in ipv4_addresses:
        v2_addresses.append(f"{ipv4_address.get('address')}/24")

    for ipv6_address in ipv6_addresses:
        v2_addresses.append(f"{ipv6_address.get('address')}/64")

    cloud_init_network_v2 = {
        "version": 2,
        "ethernets": {
        }
    }

    ipv4_gateway = ipv4_addresses[0].get('gateway')
    ipv6_gateway = ipv6_addresses[0].get('gateway') if ipv6_addresses else None
    ethernet_config = {
        "match": {
            "macaddress": mac_address
        },
        "addresses": v2_addresses,
    }
    if not explicit_routes:
        ethernet_config["gateway4"] = ipv4_gateway
    ethernet_config["nameservers"] = {"addresses": ['8.8.8.8', '8.8.4.4']}
    if ipv6_addresses and not explicit_routes:
        ethernet_config["gateway6"] = ipv6_gateway

    cloud_init_network_v2["ethernets"][network_device_name] = ethernet_config

    # CentOS and AlmaLinux continue to use only the gateway fields.
    if not is_centos:
        routes = []
        if ipv6_addresses:
            if explicit_routes:
                # A direct host route makes an off-prefix gateway reachable.
                routes.extend([
                    {"to": f"{ipv6_gateway}/128", "via": "::"},
                    {"to": "::/0", "via": ipv6_gateway},
                ])
            else:
                routes.extend([
                    {"to": ipv6_gateway, "via": "::", "on-link": True},
                    {"to": "::/0", "on-link": True, "via": ipv6_gateway},
                ])
        routes.append({
            "to": "0.0.0.0/0",
            "via": ipv4_gateway,
        })
        ethernet_config["routes"] = routes

    config_file_path = f'{os.getenv("SNIPPETS_DIR")}/{vm_id}-network.yaml'
    with open(config_file_path, 'w') as outfile:
        yaml = YAML()
        yaml.preserve_quotes = False
        yaml.explicit_start = True
        yaml.dump(cloud_init_network_v2, outfile)

    return {
        "file_path": config_file_path,
        "config": cloud_init_network_v2
    }


@app.route('/node/snippets/network/<vm_id>/apply', methods=['post'])
def snippets_network_vmid_apply_post(vm_id):
    vm_id = str(vm_id)
    result = subprocess.run([
        "qm",
        "set",
        vm_id,
        "--cicustom",
        f'network=local:snippets/{vm_id}-network.yaml'
    ])

    return { "status": "ok" }

if __name__ == "__main__":
    app.run(debug=True)
