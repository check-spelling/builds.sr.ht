(use-modules (gnu))
(use-service-modules networking shepherd ssh)
(use-package-modules certs curl gnupg ssh virtualization version-control)

(operating-system
 (host-name "build")
 (timezone "Etc/UTC")
 (bootloader (bootloader-configuration
              (bootloader grub-bootloader)
              (targets '("/dev/vda"))
              (timeout 0)))
 (initrd (lambda (file-systems . rest)
           (apply base-initrd file-systems
                  #:qemu-networking? #t
                  rest)))
 (file-systems (cons (file-system
                      (mount-point "/")
                      (device "/dev/vda1")
                      (type "ext4"))
                     %base-file-systems))
 (users (cons (user-account
               (name "build")
               (group "users")
               (password "")
               (supplementary-groups '("wheel" "kvm"))
               (uid 1000))
              %base-user-accounts))
 (sudoers-file (plain-file "sudoers" "\
root ALL=(ALL) ALL
%wheel ALL=(ALL) NOPASSWD: ALL\n"))
 (services (cons* (static-networking-service
                   "eth0" "10.0.2.15"
                   #:netmask "255.255.255.0"
                   #:gateway "10.0.2.2"
                   #:name-servers '(;; OpenNIC
                                    "185.121.177.177"
                                    "169.239.202.202"
                                    ;; Google
                                    "8.8.8.8"
                                    "8.8.4.4"))
                  (service openssh-service-type
                           (openssh-configuration
                            (permit-root-login #t)
                            (allow-empty-passwords? #t)))
                  %base-services))
 (packages (cons* curl
                  git-minimal
                  gnupg
                  mercurial
                  nss-certs
                  openssh
                  %base-packages)))
