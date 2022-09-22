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
 (services (cons* (service static-networking-service-type
			   (list (static-networking
			    (inherit %qemu-static-networking)
			    (name-servers '(;; OpenNIC
					    "134.195.4.2" ;; https://servers.opennicproject.org/edit.php?srv=ns4.any.dns.opennic.glue
					    ;; Google
					    "8.8.8.8"
					    "8.8.4.4")))))
                  (service openssh-service-type
                           (openssh-configuration
			    (openssh openssh-sans-x)
                            (permit-root-login #t)
                            (allow-empty-passwords? #t)))
                  %base-services))
 (packages (cons* curl
                  git-minimal
                  gnupg
                  mercurial
                  nss-certs
                  openssh-sans-x
                  %base-packages)))
