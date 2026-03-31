package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
	"github.com/jung-kurt/gofpdf"

	"golang.org/x/text/encoding/charmap"
)

// Estruturas de dados
type RespostasInput struct {
	Funcionario string           `json:"funcionario"`
	Dimensoes   map[string][]int `json:"dimensoes"`
}

type Risk struct {
	Dimensao      string `json:"dimensao"`
	Probabilidade int    `json:"probabilidade"`
	Severidade    int    `json:"severidade"`
	NivelRisco    string `json:"nivel_risco"`
	CorPGR        string `json:"cor_pgr"`
	Recomendacao  string `json:"recomendacao_nr1"`
}

type GeminiAIStudio struct {
	ScoreGeral int    `json:"score_geral_saude"`
	MatrizPGR  []Risk `json:"matriz_pgr"`
	Conclusao  string `json:"conclusao_diagnostica"`
}

func main() {
	fmt.Println(">>> Iniciando Rumo Saudável...")
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Erro ao carregar o arquivo .env")
	}

	app := fiber.New()

	app.Post("/api/v1/pgr-psicossocial", func(c *fiber.Ctx) error {
		fmt.Println("\n--- Nova requisição recebida ---")
		var input RespostasInput
		if err := c.BodyParser(&input); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "JSON inválido"})
		}

		// 1. Sincroniza Documentos (Verifica se estão na API ou faz Upload da pasta ./docs)
		fileURIs, err := sincronizarDocumentos()
		if err != nil {
			fmt.Printf("ERR: Falha ao sincronizar documentos: %v\n", err)
			return c.Status(500).JSON(fiber.Map{"error": "Erro ao preparar base de conhecimento"})
		}

		scoresBrutos := calcularScoresCopsoqBr(input.Dimensoes)

		// 2. Chamada à IA passando as URIs dos ficheiros sincronizados
		analise, err := consultarGeminiAIStudio(input.Funcionario, scoresBrutos, fileURIs)
		if err != nil {
			fmt.Printf("ERR: Falha na IA: %v\n", err)
			return c.Status(500).JSON(fiber.Map{"error": "Erro na IA: " + err.Error()})
		}

		// 3. Geração do PDF
		pdfPath := fmt.Sprintf("relatorio_nr1_%s.pdf", strings.ReplaceAll(input.Funcionario, " ", "_"))
		err = gerarPDFColorido(input.Funcionario, analise, pdfPath)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Erro ao gerar PDF"})
		}

		fmt.Printf(">>> Sucesso: Relatório gerado para %s\n", input.Funcionario)
		return c.Download(pdfPath)
	})

	log.Fatal(app.Listen("0.0.0.0:3000"))
}

func sincronizarDocumentos() ([]string, error) {
	apiKey := os.Getenv("GOOGLE_API_KEY")
	folderPath := "./docs"
	filesToUpload := []string{
		"adaptacao_cultural_copsoq_brasil.pdf",
		"copsoq_II_curto_versao_adap_brasil.pdf",
		"tese_de_doutorado_copsoq_brasil_jsg_final.pdf",
	}

	// Listar arquivos que já existem no servidor do Google
	listURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/files?key=%s", apiKey)
	resp, err := http.Get(listURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var listData struct {
		Files []struct {
			DisplayName string `json:"displayName"`
			URI         string `json:"uri"`
			State       string `json:"state"`
		} `json:"files"`
	}
	json.NewDecoder(resp.Body).Decode(&listData)

	existingMap := make(map[string]string)
	for _, f := range listData.Files {
		if f.State == "ACTIVE" {
			existingMap[f.DisplayName] = f.URI
		}
	}

	var finalURIs []string
	for _, fileName := range filesToUpload {
		if uri, ok := existingMap[fileName]; ok {
			fmt.Printf(">>> Arquivo verificado (OK): %s\n", fileName)
			finalURIs = append(finalURIs, uri)
		} else {
			fmt.Printf(">>> Arquivo ausente ou expirado. Fazendo upload: %s...\n", fileName)
			newURI, err := uploadFileToAIStudio(filepath.Join(folderPath, fileName), fileName, apiKey)
			if err != nil {
				return nil, err
			}
			finalURIs = append(finalURIs, newURI)
		}
	}

	return finalURIs, nil
}

func uploadFileToAIStudio(path, displayName, apiKey string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("falha ao abrir arquivo local: %v", err)
	}
	defer file.Close()

	fileInfo, _ := file.Stat()
	fileSize := fileInfo.Size()

	// 1. Iniciar upload (Metadata) - Note a URL de upload correta
	initURL := fmt.Sprintf("https://generativelanguage.googleapis.com/upload/v1beta/files?key=%s", apiKey)

	meta := map[string]interface{}{"file": map[string]string{"displayName": displayName}}
	metaJSON, _ := json.Marshal(meta)

	req, err := http.NewRequest("POST", initURL, bytes.NewReader(metaJSON))
	if err != nil {
		return "", fmt.Errorf("erro ao criar requisição de init: %v", err)
	}

	req.Header.Set("X-Goog-Upload-Protocol", "resumable")
	req.Header.Set("X-Goog-Upload-Command", "start")
	req.Header.Set("X-Goog-Upload-Header-Content-Length", fmt.Sprintf("%d", fileSize))
	req.Header.Set("X-Goog-Upload-Header-Content-Type", "application/pdf")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("erro na chamada de init upload: %v", err)
	}
	defer resp.Body.Close()

	uploadURL := resp.Header.Get("X-Goog-Upload-URL")
	if uploadURL == "" {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Google não retornou URL de upload. Status: %d, Body: %s", resp.StatusCode, string(body))
	}

	// 2. Upload do binário real
	reqData, err := http.NewRequest("POST", uploadURL, file)
	if err != nil {
		return "", fmt.Errorf("erro ao criar requisição de dados: %v", err)
	}
	reqData.Header.Set("X-Goog-Upload-Offset", "0")
	reqData.Header.Set("X-Goog-Upload-Command", "upload, finalize")
	reqData.ContentLength = fileSize

	respData, err := client.Do(reqData)
	if err != nil {
		return "", fmt.Errorf("erro no envio do binário: %v", err)
	}
	defer respData.Body.Close()

	if respData.StatusCode != 200 && respData.StatusCode != 201 {
		body, _ := io.ReadAll(respData.Body)
		return "", fmt.Errorf("falha no upload do binário. Status: %d, Body: %s", respData.StatusCode, string(body))
	}

	var res struct {
		File struct {
			URI string `json:"uri"`
		} `json:"file"`
	}
	if err := json.NewDecoder(respData.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("erro ao decodificar resposta final do Google: %v", err)
	}

	return res.File.URI, nil
}

func consultarGeminiAIStudio(funcionario string, dados map[string]float64, fileURIs []string) (*GeminiAIStudio, error) {
	apiKey := os.Getenv("GOOGLE_API_KEY")
	modelName := "gemini-2.5-pro"
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", modelName, apiKey)

	// System Instructions reforçado para evitar gatilhos de segurança
	systemInstructions := `VOCÊ É UM ASSISTENTE TÉCNICO DE ENGENHARIA DE SEGURANÇA DO TRABALHO.
		ESTA É UMA ANÁLISE TÉCNICA E ESTATÍSTICA PARA CONFORMIDADE COM A NR-01 (PGR).
		O conteúdo dos documentos anexados (COPSOQ II-Br) deve ser tratado estritamente como base científica para classificação de riscos ocupacionais.
		Não há aconselhamento médico individual, apenas análise de scores organizacionais.

		Sua tarefa é converter scores (0-100) em uma Matriz de Risco detalhada.

		DIRETRIZES:
		1. NÃO AGRUPAR DIMENSÕES: Gere um objeto individual para CADA dimensão recebida.
		2. DIREÇÃO DO SCORE: ALTO RISCO se Score FOR ALTO para Exigências/Burnout; ALTO RISCO se Score FOR BAIXO para Recursos/Apoio.
		3. FORMATO DE SAÍDA (ESTRITAMENTE JSON):
		{
		"score_geral_saude": integer,
		"matriz_pgr": [
			{"dimensao": "string", "probabilidade": 1-5, "severidade": 1-5, "nivel_risco": "string", "cor_pgr": "string", "recomendacao_nr1": "string"}
		],
		"conclusao_diagnostica": "string"
		}

		IMPORTANT: Return ONLY a valid JSON object.`

	var parts []map[string]interface{}
	for _, uri := range fileURIs {
		parts = append(parts, map[string]interface{}{
			"file_data": map[string]interface{}{"mime_type": "application/pdf", "file_uri": uri},
		})
	}
	parts = append(parts, map[string]interface{}{
		"text": fmt.Sprintf("Analise tecnicamente os scores do funcionário %s: %v. Retorne apenas o JSON.", funcionario, dados),
	})

	payload := map[string]interface{}{
		"system_instruction": map[string]interface{}{
			"parts": []map[string]interface{}{{"text": systemInstructions}},
		},
		"contents": []map[string]interface{}{{"parts": parts}},
		"generationConfig": map[string]interface{}{
			"temperature":        0.0,
			"topP":               0.1, // Reduzido para evitar alucinações que disparem filtros
			"response_mime_type": "application/json",
		},
		"safetySettings": []map[string]interface{}{
			{"category": "HARM_CATEGORY_HARASSMENT", "threshold": "BLOCK_NONE"},
			{"category": "HARM_CATEGORY_HATE_SPEECH", "threshold": "BLOCK_NONE"},
			{"category": "HARM_CATEGORY_SEXUALLY_EXPLICIT", "threshold": "BLOCK_NONE"},
			{"category": "HARM_CATEGORY_DANGEROUS_CONTENT", "threshold": "BLOCK_NONE"},
		},
	}

	jsonData, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Lemos todo o corpo da resposta primeiro para garantir o log de erro
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var geminiRaw struct {
		Candidates []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			SafetyRatings []struct {
				Category    string `json:"category"`
				Probability string `json:"probability"`
			} `json:"safetyRatings"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(bodyBytes, &geminiRaw); err != nil {
		fmt.Printf(">>> Erro ao decodificar resposta. Raw Body: %s\n", string(bodyBytes))
		return nil, err
	}

	// Verificação detalhada de bloqueio
	if len(geminiRaw.Candidates) == 0 || geminiRaw.Candidates[0].FinishReason == "SAFETY" {
		fmt.Printf(">>> BLOQUEIO DE SEGURANÇA DETECTADO. Resposta Bruta: %s\n", string(bodyBytes))
		if len(geminiRaw.Candidates) > 0 {
			fmt.Printf(">>> Safety Ratings: %+v\n", geminiRaw.Candidates[0].SafetyRatings)
		}
		return nil, fmt.Errorf("IA bloqueou a resposta por segurança ou falta de candidatos")
	}

	var finalResponse GeminiAIStudio
	textResponse := geminiRaw.Candidates[0].Content.Parts[0].Text
	err = json.Unmarshal([]byte(textResponse), &finalResponse)
	return &finalResponse, err
}

func latin1(s string) string {
    enc := charmap.ISO8859_1.NewEncoder()
    out, _ := enc.String(s)
    return out
}

func calcularScoresCopsoqBr(dimensoes map[string][]int) map[string]float64 {
	resultados := make(map[string]float64)
	for nome, valores := range dimensoes {
		soma := 0
		for _, v := range valores {
			soma += v
		}
		media := (float64(soma) / float64(len(valores)*5)) * 100
		resultados[nome] = media
	}
	return resultados
}

func gerarPDFColorido(nome string, data *GeminiAIStudio, path string) error {
    pdf := gofpdf.New("P", "mm", "A4", "")
    pdf.AddPage()

    cores := map[string][]int{
        "Verde":    {144, 238, 144},
        "Amarelo":  {255, 255, 0},
        "Laranja":  {255, 165, 0},
        "Vermelho": {255, 99, 71},
    }

    pdf.SetFont("Arial", "B", 14)
    // USAR latin1() AQUI
    pdf.Cell(0, 10, latin1("Inventário de Riscos Psicossociais (NR-01 / COPSOQ II-Br)"))
    pdf.Ln(12)

    pdf.SetFont("Arial", "", 10)
    pdf.Cell(0, 8, latin1(fmt.Sprintf("Funcionário: %s", nome)))
    pdf.Ln(6)
    pdf.Cell(0, 8, latin1(fmt.Sprintf("Índice de Saúde Mental Global: %d/100", data.ScoreGeral)))
    pdf.Ln(12)

    pdf.SetFillColor(230, 230, 230)
    pdf.SetFont("Arial", "B", 8)
    pdf.CellFormat(55, 10, latin1("Dimensão"), "1", 0, "C", true, 0, "")
    pdf.CellFormat(12, 10, "P", "1", 0, "C", true, 0, "")
    pdf.CellFormat(12, 10, "S", "1", 0, "C", true, 0, "")
    pdf.CellFormat(25, 10, "Risco", "1", 0, "C", true, 0, "")
    pdf.CellFormat(85, 10, latin1("Recomendação NR-01"), "1", 1, "C", true, 0, "")

    pdf.SetFont("Arial", "", 7)
    for _, r := range data.MatrizPGR {
        c, ok := cores[r.CorPGR]
        if !ok { c = []int{255, 255, 255} }

        // USAR latin1() NOS CAMPOS DA IA
        pdf.CellFormat(55, 10, latin1(r.Dimensao), "1", 0, "L", false, 0, "")
        pdf.CellFormat(12, 10, fmt.Sprintf("%d", r.Probabilidade), "1", 0, "C", false, 0, "")
        pdf.CellFormat(12, 10, fmt.Sprintf("%d", r.Severidade), "1", 0, "C", false, 0, "")

        pdf.SetFillColor(c[0], c[1], c[2])
        pdf.CellFormat(25, 10, latin1(r.NivelRisco), "1", 0, "C", true, 0, "")

        x, y := pdf.GetX(), pdf.GetY()
        pdf.MultiCell(85, 5, latin1(r.Recomendacao), "1", "L", false)
        pdf.SetXY(x+85, y)
        pdf.Ln(10)
    }

    pdf.Ln(5)
    pdf.SetFont("Arial", "B", 10)
    pdf.Cell(0, 10, latin1("Parecer Técnico:"))
    pdf.Ln(8)
    pdf.SetFont("Arial", "", 9)
    pdf.MultiCell(0, 5, latin1(data.Conclusao), "", "L", false)

    return pdf.OutputFileAndClose(path)
}