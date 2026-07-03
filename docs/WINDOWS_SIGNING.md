# Windows code signing (Azure Trusted Signing)

SmartScreen shows "Windows protected your PC — unknown publisher" for every
unsigned download. The fix is an **Authenticode** signature. This project signs
via **Azure Trusted Signing** (Microsoft's cloud signing service, ~$10/mo, no
hardware token — unlike a traditional OV/EV cert). The release workflow
(`.github/workflows/release.yml`, `windows-gui` job) signs `OpenDeezer.exe` when
the Azure secrets are present; without them it produces the same unsigned zip.

## One-time Azure setup

You need an Azure subscription (an Azure for Students subscription works).

1. **Register the provider**: Azure portal → your subscription → *Resource
   providers* → register **`Microsoft.CodeSigning`**.
2. **Create a Trusted Signing account** (search "Trusted Signing" → Create) in a
   supported region (East US, West US 3, West Central US, North/West Europe).
   Note the region — the signing **endpoint** is region-specific, e.g.
   `https://neu.codesigning.azure.net/` (North Europe), `https://eus.codesigning.azure.net/` (East US).
3. **Identity validation** → create it under the account:
   - **Individual** validation for a solo dev (government-ID check), or
   - **Organization** validation (legal entity, ≥3 years, D-U-N-S).
   - This gates eligibility; individual validation is available but may take a
     few days to approve. The signed publisher name is the validated identity.
4. **Certificate Profile** → create one of type **Public Trust** under the
   account (this is what chains to a Microsoft-trusted root for SmartScreen).
   Note its **name**.
5. **Service principal for CI**: Entra ID → *App registrations* → *New
   registration* → then *Certificates & secrets* → *New client secret*. Record
   the **Tenant ID**, **Client ID**, **Client secret**.
6. **Grant the signer role**: on the Trusted Signing account → *Access control
   (IAM)* → *Add role assignment* → role **Trusted Signing Certificate Profile
   Signer** → assign to the app registration from step 5.

## GitHub repository secrets

Settings → Secrets and variables → Actions. The `windows-gui` job checks for
`AZURE_CLIENT_ID` + `TRUSTED_SIGNING_ACCOUNT_NAME` to decide whether to sign.

| Secret | Value |
| --- | --- |
| `AZURE_TENANT_ID` | Entra tenant ID (step 5) |
| `AZURE_CLIENT_ID` | app registration client ID (step 5) |
| `AZURE_CLIENT_SECRET` | app registration client secret (step 5) |
| `TRUSTED_SIGNING_ENDPOINT` | region endpoint, e.g. `https://neu.codesigning.azure.net/` |
| `TRUSTED_SIGNING_ACCOUNT_NAME` | the Trusted Signing account name (step 2) |
| `TRUSTED_SIGNING_CERT_PROFILE` | the certificate profile name (step 4) |

Cut the next tag and `OpenDeezer.exe` is Authenticode-signed + RFC-3161
timestamped. SmartScreen recognises the Microsoft-rooted publisher; a brand-new
identity may still accrue reputation over the first downloads, but the "unknown
publisher" scare is gone.

## Notes

- Only the launcher `OpenDeezer.exe` is signed. The WinAppSDK DLLs are already
  Microsoft-signed and `libdeezercore.dll` is loaded by the signed exe, so
  SmartScreen (which assesses the launched exe) doesn't need every DLL signed.
- The **TUI** `opendeezer-tui-windows-amd64.exe` (built in the `tui` job) is not
  signed yet — add the same `azure/trusted-signing-action` step there if you
  distribute it as a raw download rather than via winget/scoop.
- Verify a signed build: right-click the exe → *Properties* → *Digital
  Signatures*, or `Get-AuthenticodeSignature OpenDeezer.exe` in PowerShell.
- Pin the action version (`azure/trusted-signing-action@vX.Y.Z`) once you've
  confirmed a working release, instead of the floating `@v0`.
