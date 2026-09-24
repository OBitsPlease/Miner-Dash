# Existing Linux rig pilot

Use this kit to add an existing x86-64 Linux rig without rewriting its disk.
Do not stop or remove the current mining services until the inspection output
has been reviewed and MinerDash enrollment is ready.

From Windows, copy the kit and connect:

```powershell
scp .\build\minerdash-ssh-kit.zip user@RIG_IP:/tmp/
ssh user@RIG_IP
```

On the rig:

```sh
cd /tmp
unzip minerdash-ssh-kit.zip -d minerdash-ssh-kit
cd minerdash-ssh-kit
sudo bash scripts/inspect-existing-rig.sh
sudo bash scripts/expand-root-filesystem.sh --plan
sha256sum build/minerdash-agent-amd64
sudo bash scripts/install-existing-linux.sh \
  --agent build/minerdash-agent-amd64 \
  --agent-sha256 SHA256_PRINTED_BY_THE_PREVIOUS_COMMAND
```

The installer stages MinerDash but deliberately does not stop existing mining
services, enroll the rig, or start the MinerDash service. Review the reported
disk layout and active service names before cutover.

For enrollment, obtain the controller URL, TLS fingerprint, and enrollment
token from the manual enrollment section under **Rig OS & USB**. Put the token
in a root-only file without including it in a command argument or shell history:

```sh
sudo install -o root -g root -m 0600 /dev/null /root/minerdash-token
sudo nano /root/minerdash-token
sudo /usr/local/sbin/minerdash-enroll \
  --controller https://CONTROLLER_IP:8443 \
  --fingerprint TLS_SHA256 \
  --name UNIQUE_RIG_NAME \
  --token-file /root/minerdash-token
```

Only after successful enrollment should the previous miner and management
services be stopped and disabled. Determine their exact names from the
inspection report instead of using a broad process-kill command.

If the root filesystem is a supported normal partition and the expansion plan
shows unused space, apply the expansion using the exact confirmation printed
by the plan:

```sh
sudo bash scripts/expand-root-filesystem.sh \
  --apply \
  --confirm EXPAND-/dev/ACTUAL_ROOT_PARTITION
```

This expands only the existing root partition and filesystem. It does not
reformat the USB device.
