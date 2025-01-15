Vagrant.configure("2") do |config|
	config.vm.box = "ubuntu/jammy64"

	config.vm.provider "virtualbox" do |vb|
		vb.memory = "2048"
		vb.cpus = 1
		vb.customize ["modifyvm", :id, "--natdnshostresolver1", "on"]
	end

	config.vm.define "go-librespot-build" do |node|
		node.vm.hostname = "go-librespot-build"
		node.vm.synced_folder ".provision/", "/tmp/.provision"
		node.vm.synced_folder "./", "/home/vagrant/builddocker"

		node.vm.provision :salt do |salt|
			salt.masterless = true
			salt.colorize = true
			salt.verbose = true
			salt.log_level = "info"
			salt.install_type = "stable"
			salt.version = "3006.9"
			salt.run_highstate = true
			salt.salt_call_args = ["--file-root", "/tmp/.provision"]

			salt.pillar({})
		end

		# https://dh1tw.de/2019/12/cross-compiling-golang-cgo-projects/
		# https://askubuntu.com/questions/1255707/apt-cant-find-packages-on-ubuntu-20-04-arm64-raspberry-pi-4
		node.vm.provision "shell", inline: <<-SCRIPT
			apt-get install -y binfmt-support qemu-user-static
			dpkg --add-architecture arm64
			cat > /etc/apt/sources.list.d/arm64.list <<EOF
deb [arch=arm64] http://ports.ubuntu.com/ jammy main multiverse universe
deb [arch=arm64] http://ports.ubuntu.com/ jammy-security main multiverse universe
deb [arch=arm64] http://ports.ubuntu.com/ jammy-backports main multiverse universe
deb [arch=arm64] http://ports.ubuntu.com/ jammy-updates main multiverse universe
EOF

			apt-get update
			apt-get install -y gcc-aarch64-linux-gnu
			apt-get install -y libcrypt-dev:arm64
		SCRIPT

		node.vm.provision "shell", inline: <<-SCRIPT
			if ! grep "cd /home/vagrant/builddocker" /home/vagrant/.profile ; then
				echo 'cd /home/vagrant/builddocker' >> /home/vagrant/.profile
			fi
		SCRIPT
	end
end
