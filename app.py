from flask import Flask, request, make_response
import yaml
import os
import json
import subprocess

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
    ipv4_addresses = body.get('ipv4_addresses')
    ipv6_addresses = body.get('ipv6_addresses')
    mac_address = body.get('mac_address')

    def stringit(str):
        return f'"{str}"'

    if not ipv4_addresses or not mac_address:
        return {
            "error": "ipv4_addresses and mac_address are required"
        }, 400

    network_static_assignments = []
    for ipv4_address in ipv4_addresses:
        network_static_assignments.append({
            "type": "static",
            "address": stringit(ipv4_address.get('address')),
            "netmask": stringit(ipv4_address.get('netmask')),
            "gateway": stringit(ipv4_address.get('gateway'))
        })

    for ipv6_address in ipv6_addresses:
        network_static_assignments.append({
            "type": "static6",
            "address": stringit(ipv6_address.get('address')),
            "gateway": stringit(ipv6_address.get('gateway')),
            "routes": [
                {
                    "to": stringit(ipv6_address.get('gateway'))
                }
            ]
        })

    cloud_init_network = {
        "version": 1,
        "config": [
            {
                "type": "physical",
                "name": "eth0",
                "mac_address": mac_address.lower(),
                "subnets": network_static_assignments
            },
            {
                "type": "nameserver",
                "address": [
                    stringit("8.8.8.8"),
                    stringit("8.8.4.4")
                ],
                "search": [
                    stringit("det01.hostodo.com")
                ]
            }
        ]
    }

    config_file_path = f'{os.getenv("SNIPPETS_DIR")}/{vm_id}-network.yaml'
    with open(config_file_path, 'w') as outfile:
        yaml.dump(cloud_init_network, outfile, default_flow_style=False)

    return {
        "file_path": config_file_path,
        "config": cloud_init_network
    }


@app.route('/node/snippets/network/<vm_id>/apply', methods=['post'])
def snippets_network_vmid_apply_post(vm_id):
    result = subprocess.run([
        "qm",
        "set",
        vm_id,
        "--cicustom",
        f'\"network=local:snippets/{vm_id}-network.yaml\"'
    ])

    return { "status": "ok" }

if __name__ == "__main__":
    app.run(debug=True)
