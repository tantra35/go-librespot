salt-minion-disable:
  service.disabled:
    - name: salt-minion

salt-minion-stop:
  service.dead:
    - name: salt-minion

msk-timezone:
  timezone.system:
    - name: Europe/Moscow
    - utc: True

common-pkg:
  pkg.installed:
    - pkgs:
      - mc
      - ntp
      - nmon
      - htop
      - iotop
      - iftop
      - jq

update_pkg:
  pkg.uptodate:
    - refresh : True
