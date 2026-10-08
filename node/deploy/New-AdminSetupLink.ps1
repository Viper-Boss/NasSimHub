param(
    [Parameter(Mandatory=$true)][string]$DeviceAddress,
    [Parameter(Mandatory=$true)][string]$SshHost,
    [Parameter(Mandatory=$true)][string]$HostKeyAlias,
    [Parameter(Mandatory=$true)][string]$IdentityFile,
    [Parameter(Mandatory=$true)][string]$KnownHostsFile,
    [Parameter(Mandatory=$true)][string]$OutputDirectory,
    [int]$SshPort=22
)
$ErrorActionPreference='Stop'
if($DeviceAddress -notmatch '^[A-Za-z0-9.-]+$' -or $SshHost -notmatch '^[A-Za-z0-9.-]+$') { throw 'Invalid device address' }
$key=(Resolve-Path -LiteralPath $IdentityFile).Path
$hosts=(Resolve-Path -LiteralPath $KnownHostsFile).Path
$code=& ssh -p $SshPort -i $key -o BatchMode=yes -o StrictHostKeyChecking=yes -o "HostKeyAlias=$HostKeyAlias" -o "UserKnownHostsFile=$hosts" "root@$SshHost" 'cat /var/lib/nassimhub/admin-bootstrap.txt'
if($LASTEXITCODE -ne 0) { throw 'Cannot obtain first-setup proof. If already initialized, open the normal management address and log in.' }
$code=([string]$code).Trim()
if($code -notmatch '^[A-Za-z0-9_-]{43}$') { throw 'Invalid first-setup proof' }
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$dir=(Resolve-Path -LiteralPath $OutputDirectory).Path
# Restrict this private directory before writing the one-use owner shortcut.
$acl=Get-Acl -LiteralPath $dir
$acl.SetAccessRuleProtection($true,$false)
$owner=[System.Security.Principal.WindowsIdentity]::GetCurrent().Name
$rule=New-Object System.Security.AccessControl.FileSystemAccessRule($owner,'FullControl','ContainerInherit,ObjectInherit','None','Allow')
$acl.SetAccessRule($rule)
Set-Acl -LiteralPath $dir -AclObject $acl
$target=Join-Path $dir '设置棒子管理密码.url'
[System.IO.File]::WriteAllText($target,"[InternetShortcut]`r`nURL=https://${DeviceAddress}:7581/#setup=$code`r`n",(New-Object System.Text.UTF8Encoding($false)))
$code=$null
Write-Output "已生成专用设置入口：$target"
Write-Output '双击入口，只需输入并确认管理密码。设置后入口失效，请删除该文件。以后直接访问设备的 7581 后台。'
